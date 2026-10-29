package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

type fakeKeychain map[string][]byte

// security emulates the security(1) subcommands xorp uses.
func (k fakeKeychain) security(stdin string, args ...string) (string, error) {
	notFound := errors.New("security: exit status 44 The specified item could not be found in the keychain.")
	arg := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch args[0] {
	case "find-generic-password":
		if v, ok := k[arg("-s")]; ok {
			return string(v) + "\n", nil
		}
		return "", notFound
	case "delete-generic-password":
		if _, ok := k[arg("-s")]; !ok {
			return "", notFound
		}
		delete(k, arg("-s"))
		return "", nil
	case "-i":
		m := regexp.MustCompile(`-s "([^"]+)" -X ([0-9a-f]+)`).FindStringSubmatch(stdin)
		if m == nil {
			return "", fmt.Errorf("bad -i input %q", stdin)
		}
		b, _ := hex.DecodeString(m[2])
		k[m[1]] = b
		return "", nil
	case "dump-keychain":
		var sb strings.Builder
		for svc := range k {
			fmt.Fprintf(&sb, "    \"svce\"<blob>=\"%s\"\n", svc)
		}
		return sb.String(), nil
	}
	return "", fmt.Errorf("unexpected security %v", args)
}

type harness struct {
	t        *testing.T
	a        *app
	out, err *bytes.Buffer
	kc       fakeKeychain
	// nextLogin is what the fake 'claude auth login' / 'codex login' signs in.
	nextLogin string
}

func newHarness(t *testing.T, goos string) *harness {
	t.Helper()
	h := &harness{t: t, out: &bytes.Buffer{}, err: &bytes.Buffer{}, kc: fakeKeychain{}}
	home := t.TempDir()
	h.a = &app{
		stdout: h.out, stderr: h.err, getenv: func(string) string { return "" },
		home: home, user: "paulo", goos: goos, now: func() time.Time { return fixedNow },
		lookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		security: h.kc.security,
		fetch: func(string, map[string]string) (int, []byte, string, error) {
			return 0, nil, "", errors.New("offline")
		},
	}
	h.a.runInteractive = func(path string, argv, env []string) error {
		dir := ""
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
				dir = v
			}
			if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
				dir = v
			}
		}
		if strings.HasSuffix(path, "codex") {
			return os.WriteFile(filepath.Join(dir, "auth.json"), []byte(codexLogin(h.nextLogin)), 0o600)
		}
		if goos == "darwin" {
			h.kc[liveKeychainService+"-deadbeef"] = []byte(claudeLogin(h.nextLogin))
		} else if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(claudeLogin(h.nextLogin)), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"oauthAccount":`+oauthJSON(h.nextLogin)+`}`), 0o600)
	}
	return h
}

func claudeLogin(who string) string {
	return fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"at-%s","refreshToken":"rt-%s","expiresAt":%d,"subscriptionType":"max","rateLimitTier":"default_claude_max_5x"}}`,
		who, who, fixedNow.Add(5*time.Hour).UnixMilli())
}

func oauthJSON(who string) string {
	return fmt.Sprintf(`{"accountUuid":"uuid-%s","emailAddress":"%s@example.com","organizationUuid":"org-%s","organizationName":"%s org"}`, who, who, who, who)
}

func codexLogin(who string) string {
	claims, _ := json.Marshal(map[string]any{"email": who + "@example.com", "https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": "pro"}})
	idt := "x." + strings.TrimRight(b64(claims), "=") + ".y"
	return fmt.Sprintf(`{"tokens":{"id_token":%q,"access_token":"a","account_id":"acct-%s"}}`, idt, who)
}

func b64(b []byte) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		k := 0
		for j := 0; j < 3; j++ {
			n <<= 8
			if i+j < len(b) {
				n |= uint32(b[i+j])
				k++
			}
		}
		for j := 0; j <= k; j++ {
			sb.WriteByte(enc[(n>>(18-6*uint(j)))&63])
		}
	}
	return sb.String()
}

// setLive puts a Claude login in place the way Claude Code would.
func (h *harness) setLive(who string) {
	h.t.Helper()
	if h.a.goos == "darwin" {
		h.kc[liveKeychainService] = []byte(claudeLogin(who))
	} else {
		p := filepath.Join(h.a.home, ".claude", ".credentials.json")
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(claudeLogin(who)), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
	doc := `{"numStartups": 7, "oauthAccount":` + oauthJSON(who) + `}`
	if err := os.WriteFile(filepath.Join(h.a.home, ".claude.json"), []byte(doc), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) run(args ...string) int {
	h.out.Reset()
	h.err.Reset()
	return h.a.run(args)
}

func (h *harness) liveWho() string {
	h.t.Helper()
	b, err := h.a.liveSecret(claudeTool)
	if err != nil {
		h.t.Fatal(err)
	}
	m := regexp.MustCompile(`"accessToken":"at-(\w+)"`).FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
}

func (h *harness) vaultHas(name string) bool {
	b, _ := h.a.vaultSecret(claudeTool, name)
	return len(b) > 0
}

func TestAddSwitchRoundTrip(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			h := newHarness(t, goos)
			h.setLive("work")

			if code := h.run("add", "claude", "work"); code != 0 {
				t.Fatalf("add work: %d %s", code, h.err)
			}
			if !strings.Contains(h.out.String(), "registered the current login") {
				t.Fatalf("unexpected output %q", h.out)
			}
			h.nextLogin = "personal"
			if code := h.run("add", "claude", "personal"); code != 0 {
				t.Fatalf("add personal: %d %s", code, h.err)
			}
			if h.liveWho() != "work" || !h.vaultHas("personal") || h.vaultHas("work") {
				t.Fatalf("after add: live=%s personal=%v work=%v", h.liveWho(), h.vaultHas("personal"), h.vaultHas("work"))
			}
			if goos == "darwin" && len(h.kc) != 2 {
				t.Fatalf("throwaway login item not cleaned up: %v", h.kc)
			}

			if code := h.run("switch", "claude", "personal"); code != 0 {
				t.Fatalf("switch: %d %s", code, h.err)
			}
			// Each login lives in exactly one place.
			if h.liveWho() != "personal" || h.vaultHas("personal") || !h.vaultHas("work") {
				t.Fatalf("after switch: live=%s personal=%v work=%v", h.liveWho(), h.vaultHas("personal"), h.vaultHas("work"))
			}
			b, _ := os.ReadFile(h.a.claudeJSON())
			if !strings.Contains(string(b), "personal@example.com") || !strings.Contains(string(b), `"numStartups": 7`) {
				t.Fatalf("~/.claude.json not patched in place: %s", b)
			}
			active, _ := os.ReadFile(filepath.Join(h.a.stateDir(), "active.json"))
			if string(active) != `{"claude":"personal"}` {
				t.Fatalf("active.json = %s", active)
			}

			if code := h.run("switch", "claude", "personal"); code != 0 || !strings.Contains(h.out.String(), "already on") {
				t.Fatalf("switch to active: %d %q", code, h.out)
			}
			if code := h.run("switch", "claude", "work"); code != 0 || h.liveWho() != "work" || !h.vaultHas("personal") {
				t.Fatalf("switch back: %d live=%s", code, h.liveWho())
			}
		})
	}
}

func TestSwitchRefusesToLoseUnregisteredLogin(t *testing.T) {
	h := newHarness(t, "linux")
	h.setLive("work")
	h.run("add", "claude", "work")
	h.nextLogin = "personal"
	h.run("add", "claude", "personal")
	h.setLive("stranger") // someone ran /login with another account
	if code := h.run("switch", "claude", "personal"); code != exitErr {
		t.Fatalf("want exit 1, got %d", code)
	}
	if h.liveWho() != "stranger" || !strings.Contains(h.err.String(), "not registered") {
		t.Fatalf("live=%s err=%q", h.liveWho(), h.err)
	}
}

func TestAddRejectsDuplicateAccount(t *testing.T) {
	h := newHarness(t, "linux")
	h.setLive("work")
	h.run("add", "claude", "work")
	h.nextLogin = "work"
	if code := h.run("add", "claude", "again"); code != exitErr || !strings.Contains(h.err.String(), "already account 'work'") {
		t.Fatalf("code=%d err=%q", code, h.err)
	}
}

func TestCodexAdd(t *testing.T) {
	h := newHarness(t, "darwin")
	h.nextLogin = "me"
	if code := h.run("add", "codex", "personal"); code != 0 {
		t.Fatalf("add: %d %s", code, h.err)
	}
	// No live login before: the new one becomes the live one.
	b, _ := os.ReadFile(filepath.Join(h.a.home, ".codex", "auth.json"))
	if !strings.Contains(string(b), "acct-me") {
		t.Fatalf("live codex login = %s", b)
	}
	m, err := h.a.readMeta(codexTool, "personal")
	if err != nil || m.Email != "me@example.com" || m.Plan != "pro" || m.ID != "acct-me" {
		t.Fatalf("meta = %+v, %v", m, err)
	}
}

func TestRemove(t *testing.T) {
	h := newHarness(t, "linux")
	h.setLive("work")
	h.run("add", "claude", "work")
	h.nextLogin = "personal"
	h.run("add", "claude", "personal")
	if code := h.run("remove", "claude", "personal"); code != 0 || h.vaultHas("personal") {
		t.Fatalf("remove: %d", code)
	}
	if code := h.run("remove", "claude", "work"); code != 0 || h.liveWho() != "work" {
		t.Fatalf("removing the active account must keep its login: %d live=%s", code, h.liveWho())
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t, "linux")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"switch"}, "missing tool"},
		{[]string{"switch", "cladue"}, "Did you mean 'claude'"},
		{[]string{"add", "claude"}, "missing account name"},
		{[]string{"add", "claude", "Bad Name"}, "invalid account name"},
		{[]string{"usage", "--burn", "2h"}, "--burn must be"},
		{[]string{"swtich"}, "Did you mean 'switch'"},
	}
	for _, c := range cases {
		if code := h.run(c.args...); code != exitUsage || !strings.Contains(h.err.String(), c.want) {
			t.Errorf("%v: code=%d err=%q", c.args, code, h.err)
		}
	}
	h.setLive("work")
	h.run("add", "claude", "work")
	if code := h.run("switch", "claude", "wrok"); code != exitErr || !strings.Contains(h.err.String(), "Did you mean 'work'") {
		t.Errorf("unknown account: %d %q", code, h.err)
	}
}

func ptime(t time.Time) *time.Time { return &t }

func view(name string, session, weekly float64, weeklyReset time.Time) *accountView {
	return &accountView{meta: &meta{Name: name}, limits: []limitRec{
		{Key: "five_hour", Used: session, ResetsAt: ptime(fixedNow.Add(2 * time.Hour)), WindowSeconds: 18000},
		{Key: "seven_day", Used: weekly, ResetsAt: ptime(weeklyReset), WindowSeconds: 604800},
	}}
}

func TestPickBestSoonestReset(t *testing.T) {
	h := newHarness(t, "linux")
	for _, who := range []string{"work", "personal", "spare"} {
		h.a.writeMeta(claudeTool, &meta{Name: who, identity: identity{ID: who}})
	}
	h.a.writeCache(claudeTool, "work", &usageRec{FetchedAt: fixedNow, Limits: view("", 1, 0.3, fixedNow.Add(24*time.Hour)).limits})
	h.a.writeCache(claudeTool, "personal", &usageRec{FetchedAt: fixedNow, Limits: view("", 0.2, 0.5, fixedNow.Add(72*time.Hour)).limits})
	h.a.writeCache(claudeTool, "spare", &usageRec{FetchedAt: fixedNow, Limits: view("", 0, 0.1, fixedNow.Add(96*time.Hour)).limits})
	st, _ := h.a.loadState(claudeTool)
	best, why, err := h.a.pickBest(st)
	// work resets first but its session is spent; personal is next.
	if err != nil || best.Name != "personal" || !strings.Contains(why, "3d 0h") {
		t.Fatalf("best=%v why=%q err=%v", best, why, err)
	}
}

func TestEffectiveRollsResetWindows(t *testing.T) {
	in := []limitRec{
		{Key: "five_hour", Used: 0.9, ResetsAt: ptime(fixedNow.Add(-time.Minute)), WindowSeconds: 18000},
		{Key: "seven_day", Used: 0.7, ResetsAt: ptime(fixedNow.Add(-time.Hour)), WindowSeconds: 604800},
	}
	out := effective(in, fixedNow)
	if out[0].Used != 0 || out[0].ResetsAt != nil {
		t.Errorf("session: %+v", out[0])
	}
	if out[1].Used != 0 || !out[1].ResetsAt.Equal(fixedNow.Add(-time.Hour+7*24*time.Hour)) {
		t.Errorf("weekly: %+v", out[1])
	}
}

func TestParseUsage(t *testing.T) {
	cl, err := parseClaudeUsage([]byte(`{"five_hour":{"utilization":2.0,"resets_at":"2026-10-29T16:39:59.951997+00:00"},"seven_day":{"utilization":5.0,"resets_at":"2026-11-03T22:59:59.952016+00:00"},"seven_day_opus":null,"limits":[{"key":"x"}]}`))
	if err != nil || len(cl) != 2 || cl[0].Used != 0.02 || cl[1].Key != "seven_day" || cl[1].ResetsAt.Day() != 3 {
		t.Fatalf("claude: %+v %v", cl, err)
	}
	cx, err := parseCodexUsage([]byte(`{"rate_limit":{"primary_window":{"used_percent":51,"limit_window_seconds":604800,"reset_at":1793794074},"secondary_window":null}}`))
	if err != nil || len(cx) != 1 || cx[0].Key != "seven_day" || cx[0].Used != 0.51 {
		t.Fatalf("codex: %+v %v", cx, err)
	}
}

func TestForecast(t *testing.T) {
	views := []*accountView{
		view("work", 0, 0.5, fixedNow.Add(24*time.Hour)),
		view("personal", 0, 0, fixedNow.Add(100*time.Hour)),
	}
	// 2%/h measured over 2 hours.
	samples := []sample{
		{TS: fixedNow.Add(-2 * time.Hour).Unix(), A: map[string]sampleAcct{"work": {W: map[string]float64{"seven_day": 0.46}}}},
		{TS: fixedNow.Unix(), A: map[string]sampleAcct{"work": {W: map[string]float64{"seven_day": 0.5}}}},
	}
	fc := computeForecast(views, samples, 3, fixedNow)
	if fc.BurnSource != "history" || fc.BurnPctPerHour != 2 || fc.PoolRemainingPct != 150 || fc.CapacityPct != 200 {
		t.Fatalf("forecast = %+v", fc)
	}
	// work is drained first (soonest reset): 50% + 24h at 2%/h = 98% handed back at its reset.
	if fc.NextResetAt == nil || !fc.NextResetAt.Equal(fixedNow.Add(24*time.Hour)) || fc.NextResetGainPct != 98 {
		t.Fatalf("next reset = %v +%v", fc.NextResetAt, fc.NextResetGainPct)
	}
	if fc.PoolEmptyAt == nil || fc.PoolEmptyAt.Sub(fixedNow) < 75*time.Hour {
		t.Fatalf("pool empty = %v", fc.PoolEmptyAt)
	}
}

func TestStatusFlagsHubRouting(t *testing.T) {
	h := newHarness(t, "linux")
	h.setLive("work")
	h.run("add", "claude", "work")
	_ = os.WriteFile(filepath.Join(h.a.home, ".claude", "settings.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317"}}`), 0o600)
	if code := h.run("status"); code != exitErr || !strings.Contains(h.out.String(), "routes Claude to http://127.0.0.1:8317") {
		t.Fatalf("code=%d out=%s", code, h.out)
	}
}

func TestHubMergesReadingsAcrossMachines(t *testing.T) {
	hubApp := newHarness(t, "linux").a
	h := &hubServer{a: hubApp, path: filepath.Join(hubApp.stateDir(), "hub.json"), Accounts: map[string]*hubAccount{}, Machines: map[string]*hubMachine{}}
	post := func(rep report) []hubAccount {
		body, _ := json.Marshal(rep)
		rec := httptest.NewRecorder()
		h.handleReport(rec, httptest.NewRequest("POST", "/api/report", bytes.NewReader(body)))
		var out struct{ Accounts []hubAccount }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Accounts
	}
	old := &usageRec{FetchedAt: fixedNow.Add(-time.Hour), Limits: []limitRec{{Key: "seven_day", Used: 0.1}}}
	fresh := &usageRec{FetchedAt: fixedNow, Limits: []limitRec{{Key: "seven_day", Used: 0.4}}}
	post(report{Machine: "rog", Active: map[string]string{"claude": "p"}, Accounts: []hubAccount{{Tool: "claude", ID: "p", Name: "personal", Reading: fresh}}})
	got := post(report{Machine: "mini", Active: map[string]string{"claude": "w"}, Accounts: []hubAccount{
		{Tool: "claude", ID: "p", Name: "personal", Reading: old}, {Tool: "claude", ID: "w", Name: "work"}}})
	if len(got) != 2 || got[0].Reading == nil || got[0].Reading.Limits[0].Used != 0.4 || got[0].ReadingFrom != "rog" {
		t.Fatalf("mini should get rog's fresher reading: %+v", got)
	}
	rec := httptest.NewRecorder()
	h.handleState(rec, httptest.NewRequest("GET", "/api/state", nil))
	if !strings.Contains(rec.Body.String(), `"active_on":["rog"]`) || !strings.Contains(rec.Body.String(), `"active_on":["mini"]`) {
		t.Fatalf("state = %s", rec.Body)
	}
}

func TestOutlook(t *testing.T) {
	w := func(x time.Time) string { return x.Format("Mon 15:04") }
	reset := fixedNow.Add(48 * time.Hour)
	if o := (forecast{CapacityPct: 100, PoolRemainingPct: 60, NextResetAt: &reset}).outlook(w); o != "60% left · resets Sat 12:00" {
		t.Errorf("fine: %q", o)
	}
	dry := fixedNow.Add(24 * time.Hour)
	if o := (forecast{CapacityPct: 100, PoolEmptyAt: &dry, RefillAt: &reset}).outlook(w); o != "dry Fri 12:00 → Sat 12:00" {
		t.Errorf("dry: %q", o)
	}
}
