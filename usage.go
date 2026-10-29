package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	dot = "·"

	claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	codexUsageURL  = "https://chatgpt.com/backend-api/wham/usage"

	// liveMaxAge: a cached reading younger than this is reused instead of asking again.
	// Anthropic rate-limits the usage endpoint hard, so Claude is asked less often (the
	// statusline keeps its reading fresh anyway).
	liveMaxAge       = 4 * time.Minute
	claudeLiveMaxAge = 10 * time.Minute
	sampleEvery      = 4 * time.Minute
	historyKeep      = 30 * 24 * time.Hour
	historyLimit     = 3 << 20
)

// Usage refresh modes.
const (
	cachedOnly = iota
	refreshStale
	refreshNow
)

// limitRec is one usage window. Used is the fraction consumed (0..1).
type limitRec struct {
	Key           string     `json:"key"` // five_hour | seven_day
	Used          float64    `json:"used"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
	WindowSeconds float64    `json:"window_seconds"`
}

// usageRec is a reading of one account's limits, cached per account. The Claude Code
// statusline writes the same format from the rate limits Claude Code reports.
type usageRec struct {
	FetchedAt time.Time  `json:"fetched_at"`
	Source    string     `json:"source"` // live | statusline
	Limits    []limitRec `json:"limits"`
}

func (a *app) cachePath(t *tool, name string) string {
	return filepath.Join(a.stateDir(), "usage", t.name+"-"+name+".json")
}

func (a *app) readCache(t *tool, name string) *usageRec {
	b, err := os.ReadFile(a.cachePath(t, name))
	if err != nil {
		return nil
	}
	var r usageRec
	if json.Unmarshal(b, &r) != nil || r.FetchedAt.IsZero() {
		return nil
	}
	return &r
}

func (a *app) writeCache(t *tool, name string, r *usageRec) {
	b, _ := json.Marshal(r)
	_ = writeFileAtomic(a.cachePath(t, name), b, 0o600)
}

// effective projects a reading to now: windows whose reset has passed are empty again.
func effective(in []limitRec, now time.Time) []limitRec {
	out := make([]limitRec, 0, len(in))
	for _, l := range in {
		if l.ResetsAt != nil && !l.ResetsAt.After(now) {
			l.Used = 0
			if l.Key == "seven_day" && l.WindowSeconds > 0 {
				r := *l.ResetsAt
				for !r.After(now) {
					r = r.Add(time.Duration(l.WindowSeconds) * time.Second)
				}
				l.ResetsAt = &r
			} else {
				l.ResetsAt = nil // the next window starts with the next request
			}
		}
		out = append(out, l)
	}
	return out
}

// ----------------------------------------------------------------------------- views

type accountView struct {
	meta   *meta
	active bool
	rec    *usageRec
	err    string
	limits []limitRec
}

func (v *accountView) limit(key string) *limitRec {
	for i := range v.limits {
		if v.limits[i].Key == key {
			return &v.limits[i]
		}
	}
	return nil
}

func (v *accountView) available(now time.Time) bool {
	for _, k := range []string{"five_hour", "seven_day"} {
		if l := v.limit(k); l != nil && l.Used >= 0.999 {
			return false
		}
	}
	return true
}

// betterThan: weekly reset sooner wins; unknown resets lose; ties go to more weekly room.
func (v *accountView) betterThan(o *accountView) bool {
	vw, ow := v.limit("seven_day"), o.limit("seven_day")
	vr, or := resetOf(vw), resetOf(ow)
	if vr != or {
		return vr < or
	}
	return usedOf(vw) < usedOf(ow)
}

func resetOf(l *limitRec) float64 {
	if l == nil || l.ResetsAt == nil {
		return math.Inf(1)
	}
	return float64(l.ResetsAt.Unix())
}

func usedOf(l *limitRec) float64 {
	if l == nil {
		return 0
	}
	return l.Used
}

// freeAt is when an exhausted account is usable again (nil if unknown or not exhausted).
func (v *accountView) freeAt(now time.Time) *time.Time {
	var at *time.Time
	for _, k := range []string{"five_hour", "seven_day"} {
		l := v.limit(k)
		if l == nil || l.Used < 0.999 || l.ResetsAt == nil {
			continue
		}
		if at == nil || l.ResetsAt.After(*at) {
			at = l.ResetsAt
		}
	}
	return at
}

// usageViews returns every account of the tool with its latest reading. With refresh, the
// active account is read live (its login is the live one, so its token is current).
func (a *app) usageViews(st *toolState, refresh bool) []*accountView {
	mode := cachedOnly
	if refresh {
		mode = refreshStale
	}
	return a.usageViewsMode(st, mode)
}

func maxAge(t *tool) time.Duration {
	if t == claudeTool {
		return claudeLiveMaxAge
	}
	return liveMaxAge
}

// rateLimited is a 429 from the usage endpoint; until says when to ask again.
type rateLimited struct{ until time.Time }

func (e *rateLimited) Error() string { return "usage endpoint rate-limited" }

func (a *app) backoffPath(t *tool) string {
	return filepath.Join(a.stateDir(), "usage", t.name+".retry-after")
}

func (a *app) backoffUntil(t *tool) time.Time {
	b, err := os.ReadFile(a.backoffPath(t))
	if err != nil {
		return time.Time{}
	}
	sec, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return time.Unix(sec, 0)
}

func (a *app) usageViewsMode(st *toolState, mode int) []*accountView {
	now := a.now()
	var views []*accountView
	for _, m := range st.accounts {
		v := &accountView{meta: m, active: st.active != nil && st.active.Name == m.Name, rec: a.readCache(st.tool, m.Name)}
		stale := v.rec == nil || now.Sub(v.rec.FetchedAt) > maxAge(st.tool)
		if v.active && mode != cachedOnly && (mode == refreshNow || stale) && !a.backoffUntil(st.tool).After(now) {
			rec, err := a.fetchLive(st.tool)
			var rl *rateLimited
			switch {
			case errors.As(err, &rl):
				_ = writeFileAtomic(a.backoffPath(st.tool), []byte(strconv.FormatInt(rl.until.Unix(), 10)), 0o600)
			case err != nil:
				v.err = err.Error()
			default:
				v.rec = rec
				a.writeCache(st.tool, m.Name, rec)
			}
		}
		if v.rec != nil {
			v.limits = effective(v.rec.Limits, now)
		}
		views = append(views, v)
	}
	if mode != cachedOnly {
		a.appendSample(st.tool, views, now)
	}
	return views
}

func (a *app) fetchLive(t *tool) (*usageRec, error) {
	secret, err := a.liveSecret(t)
	if err != nil || len(secret) == 0 {
		return nil, fmt.Errorf("no live login")
	}
	token, accountID, expires := accessToken(t, secret)
	if token == "" {
		return nil, fmt.Errorf("login has no access token")
	}
	if !expires.IsZero() && expires.Before(a.now().Add(time.Minute)) {
		return nil, fmt.Errorf("access token expired; %s renews it on its next run", t.bin)
	}
	headers := map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}
	target := claudeUsageURL
	if t == claudeTool {
		headers["anthropic-beta"] = "oauth-2025-04-20"
		headers["User-Agent"] = "claude-cli/2.1 (external, cli)"
	} else {
		target = codexUsageURL
		headers["ChatGPT-Account-Id"] = accountID
		headers["User-Agent"] = "codex_cli_rs"
		headers["originator"] = "codex_cli_rs"
	}
	code, body, retryAfter, err := a.fetch(target, headers)
	if err != nil {
		return nil, err
	}
	switch {
	case code == 429:
		wait := 15 * time.Minute
		if sec, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && sec > 0 {
			wait = time.Duration(sec) * time.Second
		}
		return nil, &rateLimited{a.now().Add(wait)}
	case code == 401 || code == 403:
		return nil, fmt.Errorf("usage request rejected (%d); %s renews the login on its next run", code, t.bin)
	case code != 200:
		return nil, fmt.Errorf("usage request failed (HTTP %d)", code)
	}
	var limits []limitRec
	if t == claudeTool {
		limits, err = parseClaudeUsage(body)
	} else {
		limits, err = parseCodexUsage(body)
	}
	if err != nil {
		return nil, err
	}
	return &usageRec{FetchedAt: a.now().UTC(), Source: "live", Limits: limits}, nil
}

func parseClaudeUsage(body []byte) ([]limitRec, error) {
	// Other keys (model-scoped windows, a "limits" array) have other shapes; only the
	// two account-wide windows are decoded.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("unexpected usage response: %v", err)
	}
	var out []limitRec
	for _, k := range []struct {
		key string
		win float64
	}{{"five_hour", 5 * 3600}, {"seven_day", 7 * 86400}} {
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    *string  `json:"resets_at"`
		}
		if raw := doc[k.key]; len(raw) == 0 || json.Unmarshal(raw, &w) != nil || w.Utilization == nil {
			continue
		}
		l := limitRec{Key: k.key, Used: clamp01(*w.Utilization / 100), WindowSeconds: k.win}
		if r, ok := parseISO(w.ResetsAt); ok {
			l.ResetsAt = &r
		}
		out = append(out, l)
	}
	return out, nil
}

func parseCodexUsage(body []byte) ([]limitRec, error) {
	type window struct {
		UsedPercent        float64 `json:"used_percent"`
		LimitWindowSeconds float64 `json:"limit_window_seconds"`
		ResetAt            int64   `json:"reset_at"`
	}
	var doc struct {
		RateLimit *struct {
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("unexpected usage response: %v", err)
	}
	var out []limitRec
	if doc.RateLimit == nil {
		return out, nil
	}
	for _, w := range []*window{doc.RateLimit.Primary, doc.RateLimit.Secondary} {
		if w == nil {
			continue
		}
		key := "five_hour"
		if w.LimitWindowSeconds >= 24*3600 {
			key = "seven_day"
		}
		l := limitRec{Key: key, Used: clamp01(w.UsedPercent / 100), WindowSeconds: w.LimitWindowSeconds}
		if w.ResetAt > 0 {
			r := time.Unix(w.ResetAt, 0).UTC()
			l.ResetsAt = &r
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func httpGet(url string, headers map[string]string) (int, []byte, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", fmt.Errorf("could not reach %s: %v", req.URL.Host, unwrapURLError(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, resp.Header.Get("Retry-After"), err
}

// unwrapURLError drops the 'Get "<url>":' prefix so errors stay short.
func unwrapURLError(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, "\": "); i >= 0 {
		return s[i+3:]
	}
	return s
}

// ----------------------------------------------------------------------------- history

type sampleAcct struct {
	W map[string]float64 `json:"w"`
	R map[string]int64   `json:"r,omitempty"`
}

type sample struct {
	TS int64                 `json:"ts"`
	A  map[string]sampleAcct `json:"a"`
}

func (a *app) historyPath(t *tool) string {
	return filepath.Join(a.stateDir(), "history-"+t.name+".jsonl")
}

func (a *app) readHistory(t *tool, since time.Time) []sample {
	return readSamples(a.historyPath(t), since)
}

func readSamples(path string, since time.Time) []sample {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []sample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var s sample
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.TS >= since.Unix() {
			out = append(out, s)
		}
	}
	return out
}

// appendSample records every account's weekly usage, at most once per sampleEvery.
func (a *app) appendSample(t *tool, views []*accountView, now time.Time) {
	appendSample(a.historyPath(t), views, now)
}

func appendSample(path string, views []*accountView, now time.Time) {
	recent := readSamples(path, now.Add(-sampleEvery))
	if len(recent) > 0 {
		return
	}
	s := sample{TS: now.Unix(), A: map[string]sampleAcct{}}
	for _, v := range views {
		sa := sampleAcct{W: map[string]float64{}, R: map[string]int64{}}
		for _, l := range v.limits {
			sa.W[l.Key] = math.Round(l.Used*10000) / 10000
			if l.ResetsAt != nil {
				sa.R[l.Key] = l.ResetsAt.Unix()
			}
		}
		if len(sa.W) > 0 {
			s.A[v.meta.Name] = sa
		}
	}
	if len(s.A) == 0 {
		return
	}
	line, _ := json.Marshal(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > historyLimit {
		keep := readSamples(path, now.Add(-historyKeep))
		var buf bytes.Buffer
		for _, k := range keep {
			b, _ := json.Marshal(k)
			buf.Write(append(b, '\n'))
		}
		_ = writeFileAtomic(path, buf.Bytes(), 0o600)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// ----------------------------------------------------------------------------- usage command

func (a *app) cmdUsage(args []string) (int, error) {
	o, pos, help, err := parseFlags("usage", args, []flagSpec{
		{name: "--json"}, {name: "--burn", hasArg: true, convert: burnArg},
		{name: "--refresh"}, {name: "--emails"}, {name: "--no-color"},
	})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["usage"])
		return exitOK, nil
	}
	tools, err := toolsFrom(pos)
	if err != nil {
		return 0, err
	}
	burn := 3
	if o["--burn"] != "" {
		burn, _ = strconv.Atoi(o["--burn"])
	}
	mode := refreshStale
	if o["--refresh"] != "" {
		mode = refreshNow
	}
	now := a.now()
	s := a.outStyle(o["--no-color"] != "")
	emails := o["--emails"] != ""

	type jsonAcct struct {
		Name      string     `json:"name"`
		Active    bool       `json:"active"`
		Email     string     `json:"email,omitempty"`
		Plan      string     `json:"plan,omitempty"`
		Source    string     `json:"source,omitempty"`
		FetchedAt *time.Time `json:"fetched_at,omitempty"`
		Error     string     `json:"error,omitempty"`
		Limits    []limitRec `json:"limits"`
	}
	type jsonTool struct {
		Tool     string     `json:"tool"`
		Active   string     `json:"active,omitempty"`
		Forecast *forecast  `json:"forecast,omitempty"`
		Accounts []jsonAcct `json:"accounts"`
	}
	states, allViews, err := a.collect(tools, mode)
	if err != nil {
		return 0, err
	}
	var doc []jsonTool
	var lines []string
	for _, t := range tools {
		st, views := states[t], allViews[t]
		fc := computeForecast(views, a.readHistory(t, now.Add(-time.Duration(burn)*time.Hour)), burn, now)
		jt := jsonTool{Tool: t.name, Accounts: []jsonAcct{}}
		if st.active != nil {
			jt.Active = st.active.Name
		}
		if fc.CapacityPct > 0 {
			jt.Forecast = &fc
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, forecastHeader(t, fc, s, now))
		if len(views) == 0 {
			lines = append(lines, "  "+s.dim(fmt.Sprintf("no accounts (add one: xorp add %s <name>)", t.name)))
		}
		if st.hasLive && st.active == nil {
			lines = append(lines, "  "+s.yellow("! current login "+describe(st.live)+" is not registered (xorp add "+t.name+" <name>)"))
		}
		for _, v := range views {
			ja := jsonAcct{Name: v.meta.Name, Active: v.active, Plan: v.meta.Plan, Error: v.err, Limits: v.limits}
			if ja.Limits == nil {
				ja.Limits = []limitRec{}
			}
			if emails {
				ja.Email = v.meta.Email
			}
			if v.rec != nil {
				ja.Source, ja.FetchedAt = v.rec.Source, &v.rec.FetchedAt
			}
			jt.Accounts = append(jt.Accounts, ja)
			lines = append(lines, accountLines(v, emails, s, now)...)
		}
		doc = append(doc, jt)
	}
	if o["--json"] != "" {
		b, _ := json.MarshalIndent(map[string]any{"xorp": version, "generated_at": now.UTC(), "burn_window_hours": burn, "tools": doc}, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return exitOK, nil
	}
	lines = append(lines, "", s.dim(fmt.Sprintf("burn window %dh %s forecast assumes soonest-reset switching", burn, dot)))
	fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return exitOK, nil
}

func bar(p float64, s style) string {
	n := int(math.RoundToEven(clamp01(p) * 10))
	return colorUsed(s, p, strings.Repeat("█", n)+strings.Repeat("░", 10-n))
}

func forecastHeader(t *tool, fc forecast, s style, now time.Time) string {
	title := s.bold(fmt.Sprintf("%-6s", t.title))
	if fc.CapacityPct == 0 {
		return title + "  " + s.dim("no weekly limit data yet")
	}
	sep := s.dim(" " + dot + " ")
	color := s.green
	if frac := fc.PoolRemainingPct / fc.CapacityPct; frac < 0.1 {
		color = s.red
	} else if frac < 0.25 {
		color = s.yellow
	}
	parts := []string{fmt.Sprintf("%s  %s of %s left", title, color(fmtPct(fc.PoolRemainingPct)), fmtPct(fc.CapacityPct))}
	b := fmt.Sprintf("burn %.1f%%/h", fc.BurnPctPerHour)
	switch fc.BurnSource {
	case "week_avg":
		b += s.dim(" (week avg)")
	case "none":
		b += s.dim(" (no data)")
	}
	parts = append(parts, b)
	switch {
	case !fc.runsDry():
		if fc.NextResetAt != nil {
			parts = append(parts, s.green("lasts until the reset")+fmt.Sprintf(" (%s, +%d%%)", fmtWhen(*fc.NextResetAt, now), int(math.RoundToEven(fc.NextResetGainPct))))
		} else {
			parts = append(parts, s.green("lasts 10d+"))
		}
	case !fc.PoolEmptyAt.After(now.Add(30 * time.Second)):
		parts = append(parts, s.red("dry now"))
	default:
		parts = append(parts, fmt.Sprintf("runs dry %s %s", s.yellow(fmtWhen(*fc.PoolEmptyAt, now)), s.dim("(in "+fmtDur(fc.PoolEmptyAt.Sub(now))+")")))
	}
	if fc.runsDry() && fc.RefillAt != nil {
		parts = append(parts, "back "+fmtWhen(*fc.RefillAt, now))
	}
	return strings.Join(parts, sep)
}

func accountLines(v *accountView, emails bool, s style, now time.Time) []string {
	head := []string{"  " + s.bold(v.meta.Name)}
	if emails && v.meta.Email != "" {
		head = append(head, v.meta.Email)
	}
	if v.meta.Plan != "" {
		head = append(head, s.dim(v.meta.Plan))
	}
	if v.active {
		head = append(head, s.cyan("ACTIVE"))
	}
	if !v.available(now) {
		extra := ""
		if at := v.freeAt(now); at != nil {
			extra = " until " + fmtWhen(*at, now)
		}
		head = append(head, s.yellow("at limit"+extra))
	}
	if v.rec != nil && now.Sub(v.rec.FetchedAt) > 2*time.Minute {
		head = append(head, s.dim("as of "+fmtDur(now.Sub(v.rec.FetchedAt))+" ago"))
	}
	lines := []string{strings.Join(head, "  ")}
	for _, kn := range [][2]string{{"five_hour", "Session"}, {"seven_day", "Weekly"}} {
		l := v.limit(kn[0])
		if l == nil {
			continue
		}
		rst := "not started"
		if l.ResetsAt != nil {
			rst = "resets in " + fmtDur(l.ResetsAt.Sub(now))
		}
		lines = append(lines, fmt.Sprintf("    %-8s %s %4s used %s %s", kn[1], bar(l.Used, s), fmtPct(l.Used*100), s.dim(dot), rst))
	}
	if v.rec == nil {
		lines = append(lines, "    "+s.dim("no usage data yet (shows up after it is active once)"))
	}
	if v.err != "" {
		lines = append(lines, "    "+s.red("live check failed: ")+v.err)
	}
	return lines
}

// ----------------------------------------------------------------------------- time / format helpers

// parseISO accepts RFC 3339 with any fraction length.
func parseISO(s *string) (time.Time, bool) {
	if s == nil || *s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func fmtDur(d time.Duration) string {
	m := int(math.RoundToEven(d.Minutes()))
	if m < 0 {
		m = 0
	}
	days, h, mm := m/1440, (m%1440)/60, m%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, mm)
	case m > 0:
		return fmt.Sprintf("%dm", mm)
	}
	return "<1m"
}

// fmtWhen formats a local time; dates more than 6 days out include the day and month.
func fmtWhen(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Sub(now) > 6*24*time.Hour {
		return t.Format("Mon 02 Jan 15:04")
	}
	return t.Format("Mon 15:04")
}

func fmtPct(x float64) string { return fmt.Sprintf("%d%%", int(math.RoundToEven(x))) }

func clamp01(x float64) float64 { return math.Min(1, math.Max(0, x)) }
