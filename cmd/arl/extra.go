package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ----------------------------------------------------------------------------- daily use

// dailyUse turns weekly-usage samples into how much of a weekly quota each account
// used per local day, for the last days days (oldest first).
func dailyUse(samples []sample, days int, now time.Time) ([]time.Time, map[string][]float64) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	starts := make([]time.Time, days)
	for i := range starts {
		starts[i] = today.AddDate(0, 0, i-days+1)
	}
	out := map[string][]float64{}
	for i := 1; i < len(samples); i++ {
		t := time.Unix(samples[i].TS, 0).In(loc)
		day := int(math.Floor(t.Sub(starts[0]).Hours() / 24))
		if day < 0 || day >= days {
			continue
		}
		for name, b := range samples[i].A {
			a, ok := samples[i-1].A[name]
			wa, okA := a.W["seven_day"]
			wb, okB := b.W["seven_day"]
			if !ok || !okA || !okB {
				continue
			}
			used := 0.0
			if ra, rb := a.R["seven_day"], b.R["seven_day"]; ra > 0 && rb > 0 && rb-ra > 3600 {
				used = math.Max(0, wb) // the window rolled over
			} else if d := wb - wa; d > 0 {
				used = d
			}
			if out[name] == nil {
				out[name] = make([]float64, days)
			}
			out[name][day] += used
		}
	}
	return starts, out
}

type historyDoc struct {
	Tool     string               `json:"tool"`
	Days     []string             `json:"days"`
	Accounts map[string][]float64 `json:"accounts"`
}

func makeHistoryDoc(t *tool, samples []sample, days int, now time.Time) historyDoc {
	starts, use := dailyUse(samples, days, now)
	doc := historyDoc{Tool: t.name, Accounts: use}
	for _, s := range starts {
		doc.Days = append(doc.Days, s.Format("2006-01-02"))
	}
	for k, v := range doc.Accounts {
		for i := range v {
			v[i] = math.Round(v[i]*10000) / 10000
		}
		doc.Accounts[k] = v
	}
	return doc
}

func (h *hubServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	t, err := parseTool(r.URL.Query().Get("tool"))
	if err != nil {
		http.Error(w, "tool must be claude or codex", http.StatusBadRequest)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 30 {
		days = 7
	}
	now := h.a.now()
	if off, err := strconv.Atoi(r.URL.Query().Get("tz")); err == nil && off > -900 && off < 900 {
		now = now.In(time.FixedZone("client", off*60))
	}
	writeJSON(w, makeHistoryDoc(t, readSamples(h.historyPath(t), sinceDays(now, days+1)), days, now))
}

func (a *app) cmdHistory(args []string) (int, error) {
	o, pos, help, err := parseFlags("history", args, []flagSpec{
		{name: "--days", hasArg: true}, {name: "--json"}, {name: "--no-color"},
	})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["history"])
		return exitOK, nil
	}
	tools, err := toolsFrom(pos)
	if err != nil {
		return 0, err
	}
	days := 7
	if v := o["--days"]; v != "" {
		if days, err = strconv.Atoi(v); err != nil || days < 1 || days > 30 {
			return 0, &usageError{fmt.Sprintf("--days must be 1-30 (got '%s')", v)}
		}
	}
	now := a.now()
	s := a.outStyle(o["--no-color"] != "")
	var docs []historyDoc
	for _, t := range tools {
		doc, err := a.fetchHistory(t, days, now)
		if err != nil {
			a.note("hub history unavailable (%v); showing this machine's", err)
			doc = makeHistoryDoc(t, a.readHistory(t, sinceDays(now, days+1)), days, now)
		}
		docs = append(docs, doc)
	}
	if o["--json"] != "" {
		b, _ := json.MarshalIndent(docs, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return exitOK, nil
	}
	var lines []string
	for _, doc := range docs {
		t, _ := parseTool(doc.Tool)
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		head := fmt.Sprintf("%-12s", "")
		for _, d := range doc.Days {
			day, _ := time.ParseInLocation("2006-01-02", d, now.Location())
			head += fmt.Sprintf(" %4s", day.Format("Mon")[:3])
		}
		lines = append(lines, s.bold(t.title)+s.dim("  share of a weekly quota used per day"), s.dim(head+"  total"))
		names := make([]string, 0, len(doc.Accounts))
		for n := range doc.Accounts {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) == 0 {
			lines = append(lines, "  "+s.dim("no history yet"))
		}
		for _, n := range names {
			row, total := fmt.Sprintf("  %-10s", n), 0.0
			for _, v := range doc.Accounts[n] {
				total += v
				cell := "·"
				if v >= 0.005 {
					cell = fmt.Sprintf("%d%%", int(v*100+0.5))
				}
				row += " " + colorDay(s, v, fmt.Sprintf("%4s", cell))
			}
			lines = append(lines, row+fmt.Sprintf("  %4s", fmtPct(total*100)))
		}
	}
	fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return exitOK, nil
}

// colorDay: a day using a seventh of the quota is on pace; more is fast.
func colorDay(s style, v float64, text string) string {
	switch {
	case v < 0.005:
		return s.dim(text)
	case v > 2.0/7:
		return s.red(text)
	case v > 1.0/7:
		return s.yellow(text)
	}
	return s.green(text)
}

func (a *app) fetchHistory(t *tool, days int, now time.Time) (historyDoc, error) {
	base := a.hubURL()
	if base == "" {
		return historyDoc{}, fmt.Errorf("no hub configured")
	}
	_, off := now.Zone()
	u := fmt.Sprintf("%s/api/history?tool=%s&days=%d&tz=%d", base, t.name, days, off/60)
	code, body, _, err := a.fetch(u, map[string]string{"Accept": "application/json"})
	if err != nil {
		return historyDoc{}, err
	}
	if code != 200 {
		return historyDoc{}, fmt.Errorf("HTTP %d", code)
	}
	var doc historyDoc
	return doc, json.Unmarshal(body, &doc)
}

// ----------------------------------------------------------------------------- T3 Code compatibility

// handleAuthFiles lists the hub's accounts the way CLIProxyAPI lists its auth files.
func (h *hubServer) handleAuthFiles(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	keys := make([]string, 0, len(h.Accounts))
	for k := range h.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	files := []map[string]any{}
	for _, k := range keys {
		ha := h.Accounts[k]
		f := map[string]any{"id": ha.Tool + "-" + ha.Name, "auth_index": k, "provider": ha.Tool, "label": ha.Name}
		if ha.Email != "" {
			f["email"] = ha.Email
		}
		if ha.Tool == "codex" {
			f["id_token"] = map[string]any{"chatgpt_account_id": ha.ID, "chatgpt_plan_type": ha.Plan}
		}
		files = append(files, f)
	}
	writeJSON(w, map[string]any{"files": files})
}

// handleAPICall answers CLIProxyAPI's "call the provider as this account" with the
// account's latest reading, in the provider's own response format. Nothing is sent
// upstream: the hub holds no tokens.
func (h *hubServer) handleAPICall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AuthIndex string `json:"auth_index"`
		URL       string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reply := func(code int, body any) {
		b, _ := json.Marshal(body)
		writeJSON(w, map[string]any{"status_code": code, "body": string(b)})
	}
	now := h.a.now()
	h.mu.Lock()
	ha := h.Accounts[req.AuthIndex]
	var limits []limitRec
	if ha != nil && ha.Reading != nil {
		limits = effective(ha.Reading.Limits, now)
	}
	h.mu.Unlock()
	u, _ := url.Parse(req.URL)
	switch {
	case ha == nil:
		reply(404, map[string]any{"error": "unknown account"})
	case u == nil || (!strings.HasSuffix(u.Path, "/api/oauth/usage") && !strings.HasSuffix(u.Path, "/wham/usage")):
		reply(404, map[string]any{"error": "not available through agent-relay"})
	case limits == nil:
		reply(503, map[string]any{"error": "no reading yet"})
	case ha.Tool == "claude":
		doc := map[string]any{}
		for _, l := range limits {
			win := map[string]any{"utilization": math.Round(l.Used*1000) / 10, "resets_at": nil}
			if l.ResetsAt != nil {
				win["resets_at"] = l.ResetsAt.UTC().Format(time.RFC3339)
			}
			doc[l.Key] = win
		}
		reply(200, doc)
	default:
		win := func(l limitRec) map[string]any {
			m := map[string]any{"used_percent": math.Round(l.Used * 100), "limit_window_seconds": l.WindowSeconds}
			if l.ResetsAt != nil {
				m["reset_at"] = l.ResetsAt.Unix()
			}
			return m
		}
		rl := map[string]any{"primary_window": nil, "secondary_window": nil}
		sort.Slice(limits, func(i, j int) bool { return limits[i].WindowSeconds < limits[j].WindowSeconds })
		for i, l := range limits {
			if i == 0 {
				rl["primary_window"] = win(l)
			} else {
				rl["secondary_window"] = win(l)
			}
		}
		reply(200, map[string]any{"plan_type": ha.Plan, "rate_limit": rl})
	}
}

// ----------------------------------------------------------------------------- update

func (a *app) distDir() string { return filepath.Join(a.dataDir(), "dist") }

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// handleDownload serves the binaries published into ~/.local/share/agent-relay/dist
// ('make publish'), as arl-<os>-<arch>, with their SHA-256 as the ETag.
func (h *hubServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	target := r.PathValue("target")
	if !nameRe.MatchString(target) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(h.a.distDir(), "arl-"+target)
	sum, err := fileSHA(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	etag := `"` + sum + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

func (a *app) cmdUpdate(args []string) (int, error) {
	o, pos, help, err := parseFlags("update", args, []flagSpec{{name: "--check"}})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["update"])
		return exitOK, nil
	}
	if len(pos) > 0 {
		return 0, &usageError{fmt.Sprintf("'arl update' takes no arguments (got '%s')", pos[0])}
	}
	if o["--check"] != "" {
		newer, err := a.updateAvailable()
		if err != nil {
			return 0, &runtimeError{err.Error(), ""}
		}
		if newer {
			fmt.Fprintln(a.stdout, "an update is available: arl update")
			return exitOK, nil
		}
		fmt.Fprintln(a.stdout, "up to date")
		return exitOK, nil
	}
	if _, err := a.selfUpdate(false); err != nil {
		return 0, err
	}
	return exitOK, nil
}

func (a *app) updateTarget() (exe, target string, err error) {
	if a.hubURL() == "" {
		return "", "", &runtimeError{"no hub configured", "put its URL in ~/.config/agent-relay/hub (or set ARL_HUB)"}
	}
	exe, err = os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", "", fmt.Errorf("could not find this executable: %v", err)
	}
	return exe, runtime.GOOS + "-" + runtime.GOARCH, nil
}

func (a *app) downloadRequest(exe, target string) (*http.Response, error) {
	sum, _ := fileSHA(exe)
	req, err := http.NewRequest("GET", a.hubURL()+"/download/"+target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("If-None-Match", `"`+sum+`"`)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %s", a.hubURL(), unwrapURLError(err))
	}
	return resp, nil
}

func (a *app) updateAvailable() (bool, error) {
	exe, target, err := a.updateTarget()
	if err != nil {
		return false, err
	}
	resp, err := a.downloadRequest(exe, target)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return false, nil
	case http.StatusOK:
		return true, nil
	}
	return false, fmt.Errorf("the hub has no build for %s (HTTP %d)", target, resp.StatusCode)
}

// selfUpdate replaces this executable with the hub's build when it differs.
func (a *app) selfUpdate(quiet bool) (bool, error) {
	exe, target, err := a.updateTarget()
	if err != nil {
		return false, err
	}
	resp, err := a.downloadRequest(exe, target)
	if err != nil {
		return false, &runtimeError{err.Error(), ""}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		if !quiet {
			fmt.Fprintln(a.stdout, "arl is up to date")
		}
		return false, nil
	case http.StatusOK:
	default:
		return false, &runtimeError{fmt.Sprintf("the hub has no build for %s (HTTP %d)", target, resp.StatusCode), "publish one on the hub with 'make publish'"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return false, &runtimeError{"download failed: " + err.Error(), ""}
	}
	if sum := sha256.Sum256(body); `"`+hex.EncodeToString(sum[:])+`"` != resp.Header.Get("ETag") {
		return false, &runtimeError{"download failed: checksum mismatch", ""}
	}
	if err := writeFileAtomic(exe, body, 0o755); err != nil {
		return false, &runtimeError{"could not replace " + exe + ": " + err.Error(), ""}
	}
	fmt.Fprintf(a.stdout, "arl updated (%s)\n", exe)
	return true, nil
}
