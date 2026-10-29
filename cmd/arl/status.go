package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type check struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (a *app) cmdStatus(args []string) (int, error) {
	o, pos, help, err := parseFlags("status", args, []flagSpec{{name: "--json"}, {name: "--no-color"}})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["status"])
		return exitOK, nil
	}
	if len(pos) > 0 {
		return 0, &usageError{fmt.Sprintf("'arl status' takes no arguments (got '%s')", pos[0])}
	}
	now := a.now()
	s := a.outStyle(o["--no-color"] != "")
	type toolJSON struct {
		Tool         string `json:"tool"`
		Active       string `json:"active,omitempty"`
		Accounts     int    `json:"accounts"`
		LoginStore   string `json:"login_store"`
		TokenExpires string `json:"token_expires,omitempty"`
	}
	var tj []toolJSON
	var checks []check
	var lines []string
	for _, t := range allTools {
		st, err := a.loadState(t)
		if err != nil {
			return 0, err
		}
		store := a.liveFile(t)
		if a.usesKeychain(t) {
			store = "Keychain: " + liveKeychainService
		}
		j := toolJSON{Tool: t.name, Accounts: len(st.accounts), LoginStore: store}
		parts := []string{s.bold(fmt.Sprintf("%-7s", t.title))}
		switch {
		case st.active != nil:
			j.Active = st.active.Name
			parts = append(parts, "active "+s.green(st.active.Name)+s.dim(" ("+describe(st.live)+")"))
		case st.hasLive:
			parts = append(parts, s.yellow("login not registered")+s.dim(" ("+describe(st.live)+")"))
			checks = append(checks, check{false, fmt.Sprintf("the current %s login is not registered", t.name), fmt.Sprintf("arl add %s <name>", t.name)})
		default:
			parts = append(parts, s.dim("not logged in"))
		}
		if _, secret, ok, _ := a.liveIdentity(t); ok {
			if _, _, exp := accessToken(t, secret); !exp.IsZero() {
				j.TokenExpires = exp.UTC().Format(time.RFC3339)
				if exp.After(now) {
					parts = append(parts, "token valid "+fmtDur(exp.Sub(now)))
				} else {
					parts = append(parts, s.dim("token expired (renews on next run)"))
				}
			}
		}
		parts = append(parts, fmt.Sprintf("%d account%s", len(st.accounts), map[bool]string{true: "", false: "s"}[len(st.accounts) == 1]))
		lines = append(lines, strings.Join(parts, s.dim(" "+dot+" ")))
		for _, m := range st.accounts {
			if st.active != nil && st.active.Name == m.Name {
				continue
			}
			if b, err := a.vaultSecret(t, m.Name); err != nil || len(b) == 0 {
				checks = append(checks, check{false, fmt.Sprintf("%s account '%s' has no stored login", t.name, m.Name),
					fmt.Sprintf("arl remove %s %s && arl add %s %s", t.name, m.Name, t.name, m.Name)})
			}
		}
		tj = append(tj, j)
	}
	checks = append(checks, a.routingChecks()...)

	bad := 0
	for _, c := range checks {
		if !c.OK {
			bad++
		}
	}
	if o["--json"] != "" {
		if checks == nil {
			checks = []check{}
		}
		b, _ := json.MarshalIndent(map[string]any{"ok": bad == 0, "tools": tj, "checks": checks}, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
	} else {
		lines = append(lines, "")
		for _, c := range checks {
			mark := s.green("ok")
			if !c.OK {
				mark = s.red("!!")
			}
			lines = append(lines, fmt.Sprintf("%s  %s", mark, c.Message))
			if c.Hint != "" {
				lines = append(lines, "    "+s.dim(c.Hint))
			}
		}
		fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	}
	if bad > 0 {
		return exitErr, nil
	}
	return exitOK, nil
}

var modelProviderRe = regexp.MustCompile(`(?m)^\s*model_provider\s*=\s*"([^"]*)"`)

// routingChecks make sure the CLIs talk to their own services with their own login.
func (a *app) routingChecks() []check {
	var out []check
	routed := []string{}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if a.getenv(k) != "" {
			routed = append(routed, k)
		}
	}
	if len(routed) > 0 {
		out = append(out, check{false, "this shell sets " + strings.Join(routed, ", ") + ": Claude would not use the account login",
			"remove them from your shell profile (e.g. ~/.zshenv)"})
	} else {
		out = append(out, check{true, "no Anthropic endpoint or key overrides in the environment", ""})
	}
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if v := a.getenv(k); v != "" {
			out = append(out, check{false, fmt.Sprintf("%s=%s: this shell does not use the login arl switches", k, v), "unset it"})
		}
	}
	if b, err := os.ReadFile(filepath.Join(a.home, ".claude", "settings.json")); err == nil {
		var doc struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(b, &doc) == nil && (doc.Env["ANTHROPIC_BASE_URL"] != "" || doc.Env["ANTHROPIC_AUTH_TOKEN"] != "") {
			out = append(out, check{false, "~/.claude/settings.json routes Claude to " + orDefault(doc.Env["ANTHROPIC_BASE_URL"], "a custom endpoint"),
				"remove ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN from its \"env\" block"})
		} else {
			out = append(out, check{true, "Claude Code settings use the account login", ""})
		}
	}
	if b, err := os.ReadFile(filepath.Join(a.home, ".codex", "config.toml")); err == nil {
		if m := modelProviderRe.FindSubmatch(b); m != nil && string(m[1]) != "openai" {
			out = append(out, check{false, fmt.Sprintf("~/.codex/config.toml uses model_provider %q", m[1]),
				"remove the model_provider line to use the ChatGPT login"})
		} else {
			out = append(out, check{true, "Codex config uses the ChatGPT login", ""})
		}
	}
	return out
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// ----------------------------------------------------------------------------- omarchy

const omarchyCollector = "/usr/share/omarchy/bin/omarchy-agent-usage-%s"

var omarchyStatKeys = []string{"recentDays", "todayPrompts", "todaySessions", "todayTotalTokens", "todayTokensByModel",
	"totalPrompts", "totalSessions", "activeDays", "activeDates", "modelUsage"}

// cmdOmarchy writes one record per tool for the Omarchy Agents panel (schemaVersion 1):
// a row per account and window, the active account and the forecast in the subtitle, and
// the token stats from Omarchy's own collector. Run by arl-omarchy.timer.
func (a *app) cmdOmarchy(args []string) (int, error) {
	_, _, help, err := parseFlags("omarchy", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["omarchy"])
		return exitOK, nil
	}
	dir := filepath.Join(a.home, ".local", "state", "omarchy", "agents", "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	now := a.now()
	states, allViews, err := a.collect(allTools, refreshStale)
	if err != nil {
		return 0, err
	}
	for _, t := range allTools {
		st, views := states[t], allViews[t]
		if len(st.accounts) == 0 {
			continue
		}
		fc := computeForecast(views, a.readHistory(t, now.Add(-3*time.Hour)), 3, now)
		rec := a.omarchyRecord(t, st, views, fc, now)
		b, _ := json.MarshalIndent(rec, "", "  ")
		if err := writeFileAtomic(filepath.Join(dir, "arl-"+t.name+".json"), b, 0o644); err != nil {
			return 0, err
		}
	}
	// Records from earlier versions.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if (strings.HasPrefix(e.Name(), "hub-") || strings.HasPrefix(e.Name(), legacyName+"-")) && strings.HasSuffix(e.Name(), ".json") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return exitOK, nil
}

func (a *app) omarchyRecord(t *tool, st *toolState, views []*accountView, fc forecast, now time.Time) map[string]any {
	// The panel header has room for about 30 characters next to the name.
	tier := []string{}
	if st.active != nil {
		tier = append(tier, st.active.Name)
	}
	switch {
	case fc.CapacityPct == 0:
	case fc.runsDry():
		tier = append(tier, "dry "+shortWhen(*fc.PoolEmptyAt, now))
	case fc.NextResetAt != nil:
		tier = append(tier, "resets "+shortWhen(*fc.NextResetAt, now))
	}
	var limits []map[string]any
	var errs []string
	for _, v := range views {
		if v.err != "" {
			errs = append(errs, v.meta.Name+": "+v.err)
		}
		for _, kn := range [][2]string{{"five_hour", "Session"}, {"seven_day", "Weekly"}} {
			l := v.limit(kn[0])
			if l == nil {
				continue
			}
			// One account: the panel's own "Session"/"Weekly" titles. Several: the
			// account name first, with a short window name so it fits the row.
			title := kn[1]
			if len(views) > 1 {
				title = v.meta.Name + " " + map[string]string{"Session": "5h", "Weekly": "week"}[kn[1]]
				if v.active {
					title = "• " + title
				}
			}
			row := map[string]any{"label": title, "title": title, "percent": l.Used}
			if l.ResetsAt != nil {
				row["resetsAt"] = l.ResetsAt.UTC().Format(time.RFC3339)
			}
			limits = append(limits, row)
		}
	}
	rec := map[string]any{
		"schemaVersion": 1, "id": "arl-" + t.name, "name": t.title,
		"updatedAt": now.UTC().Format(time.RFC3339), "ready": len(limits) > 0, "scope": "account",
		"tierLabel": strings.Join(tier, " · "),
		// The panel treats a non-empty status as an error and shows authHelpText.
		"usageStatusText": "", "authHelpText": strings.Join(errs, "; "), "limits": limits,
	}
	if len(errs) > 0 && len(limits) == 0 {
		rec["usageStatusText"] = "Usage check failed"
	}
	if limits == nil {
		rec["limits"] = []any{}
	}
	stats := a.omarchyStats(t)
	rec["hasLocalStats"] = len(stats) > 0
	for k, v := range stats {
		rec[k] = v
	}
	return rec
}

// omarchyStats takes the token stats from Omarchy's collector. Its Claude collector also
// probes the usage endpoint with the login it finds, which burns Anthropic's tight rate
// limit, so it gets a config dir with the transcripts but no login.
func (a *app) omarchyStats(t *tool) map[string]json.RawMessage {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, fmt.Sprintf(omarchyCollector, t.name))
	if t == claudeTool {
		dir := filepath.Join(a.stateDir(), "omarchy-claude")
		_ = os.MkdirAll(dir, 0o700)
		for _, name := range []string{"projects", "stats-cache.json", "history.jsonl"} {
			link := filepath.Join(dir, name)
			if _, err := os.Lstat(link); err != nil {
				_ = os.Symlink(filepath.Join(a.home, ".claude", name), link)
			}
		}
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(out, &all) != nil {
		return nil
	}
	stats := map[string]json.RawMessage{}
	for _, k := range omarchyStatKeys {
		if v, ok := all[k]; ok {
			stats[k] = v
		}
	}
	return stats
}

// shortWhen is "15:04" for later today, else "Wed 15:04".
func shortWhen(t, now time.Time) string {
	t, now = t.In(now.Location()), now.In(now.Location())
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}
