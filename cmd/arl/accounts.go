package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *app) toolAndName(cmd string, pos []string, needName bool) (*tool, string, error) {
	switch {
	case len(pos) == 0:
		return nil, "", &usageError{fmt.Sprintf("missing tool: 'arl %s claude|codex%s'", cmd, map[bool]string{true: " <account>", false: " [account]"}[needName])}
	case len(pos) > 2:
		return nil, "", &usageError{fmt.Sprintf("too many arguments for 'arl %s'", cmd)}
	}
	t, err := parseTool(pos[0])
	if err != nil {
		return nil, "", err
	}
	if len(pos) == 1 {
		if needName {
			return nil, "", &usageError{fmt.Sprintf("missing account name: 'arl %s %s <account>'", cmd, t.name)}
		}
		return t, "", nil
	}
	if err := checkName(pos[1]); err != nil {
		return nil, "", err
	}
	return t, pos[1], nil
}

func accountNames(list []*meta) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.Name)
	}
	return out
}

func (a *app) noSuchAccount(st *toolState, name string) error {
	msg := fmt.Sprintf("no %s account named '%s'", st.tool.name, name)
	if s := suggest(name, accountNames(st.accounts)); s != "" {
		msg += fmt.Sprintf(". Did you mean '%s'?", s)
	}
	hint := fmt.Sprintf("see 'arl list'; add one with 'arl add %s %s'", st.tool.name, name)
	return &runtimeError{msg, hint}
}

// ----------------------------------------------------------------------------- switch

func (a *app) cmdSwitch(args []string) (int, error) {
	_, pos, help, err := parseFlags("switch", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["switch"])
		return exitOK, nil
	}
	t, name, err := a.toolAndName("switch", pos, false)
	if err != nil {
		return 0, err
	}
	st, err := a.loadState(t)
	if err != nil {
		return 0, err
	}
	if len(st.accounts) == 0 {
		return 0, &runtimeError{fmt.Sprintf("no %s accounts registered yet", t.name),
			fmt.Sprintf("register the current login with 'arl add %s <name>'", t.name)}
	}
	var target *meta
	why := ""
	if name != "" {
		if target = findAccount(st.accounts, name); target == nil {
			return 0, a.noSuchAccount(st, name)
		}
	} else {
		if target, why, err = a.pickBest(st); err != nil {
			return 0, err
		}
	}
	if st.active != nil && st.active.Name == target.Name {
		msg := fmt.Sprintf("%s: already on '%s'", t.name, target.Name)
		if why != "" {
			msg += " (" + why + ")"
		}
		fmt.Fprintln(a.stdout, msg)
		return exitOK, nil
	}
	if err := a.switchTo(st, target); err != nil {
		return 0, err
	}
	from := "no account"
	if st.active != nil {
		from = "'" + st.active.Name + "'"
	}
	msg := fmt.Sprintf("%s: switched from %s to '%s'", t.name, from, target.Name)
	if why != "" {
		msg += " (" + why + ")"
	}
	fmt.Fprintln(a.stdout, msg)
	fmt.Fprintf(a.stderr, "  running %s sessions use it from their next request\n", t.title)
	return exitOK, nil
}

// switchTo moves the live login back into the vault and the target's login out of it.
func (a *app) switchTo(st *toolState, target *meta) error {
	t := st.tool
	if st.hasLive && st.active == nil {
		who := st.live.Email
		if who == "" {
			who = "unknown account"
		}
		return &runtimeError{fmt.Sprintf("the current %s login (%s) is not registered, switching would lose it", t.name, who),
			fmt.Sprintf("register it first: arl add %s <name>", t.name)}
	}
	secret, err := a.vaultSecret(t, target.Name)
	if err != nil {
		return &runtimeError{fmt.Sprintf("could not read the stored login for '%s': %v", target.Name, err), ""}
	}
	if len(secret) == 0 {
		return &runtimeError{fmt.Sprintf("no stored login for '%s'", target.Name),
			fmt.Sprintf("log it in again: arl remove %s %s && arl add %s %s", t.name, target.Name, t.name, target.Name)}
	}
	if st.active != nil {
		live, err := a.liveSecret(t)
		if err != nil || len(live) == 0 {
			return &runtimeError{fmt.Sprintf("could not read the live %s login: %v", t.title, err), ""}
		}
		if err := a.setVaultSecret(t, st.active.Name, live); err != nil {
			return &runtimeError{fmt.Sprintf("could not store the login of '%s': %v", st.active.Name, err), ""}
		}
		if t == claudeTool {
			if oa := a.liveOAuthAccount(); oa != nil {
				st.active.OAuthAccount = oa
			}
		}
		st.active.identity = st.live
		_ = a.writeMeta(t, st.active)
	}
	if err := a.setLiveSecret(t, secret); err != nil {
		if st.active != nil {
			_ = a.deleteVaultSecret(t, st.active.Name) // the live login is still the old one
		}
		return &runtimeError{fmt.Sprintf("could not activate '%s': %v", target.Name, err), ""}
	}
	if t == claudeTool {
		if err := a.setLiveOAuthAccount(target.OAuthAccount); err != nil {
			a.note("could not update %s: %v", a.claudeJSON(), err)
		}
	}
	if err := a.deleteVaultSecret(t, target.Name); err != nil {
		a.note("could not remove the stored copy of '%s': %v", target.Name, err)
	}
	a.saveActive()
	return nil
}

// pickBest picks the account for 'switch <tool>' without a name: the first available
// one in the configured order, else soonest-reset:
// the available account whose weekly limit resets first (its leftover quota would be
// lost soonest).
func (a *app) pickBest(st *toolState) (*meta, string, error) {
	now := a.now()
	_, all, err := a.collect([]*tool{st.tool}, refreshStale)
	if err != nil {
		return nil, "", err
	}
	views := all[st.tool]
	s := a.loadSettings()
	order, where := a.orderFor(st.tool, s)
	best := choose(views, order, s[st.tool.name].limitFrac(), "")
	if best == nil {
		msg := fmt.Sprintf("every %s account is at its limit", st.tool.name)
		if len(order) > 0 {
			msg = fmt.Sprintf("every %s account in the order (%s) is at its limit", st.tool.name, strings.Join(order, ", "))
		}
		var first *time.Time
		firstName := ""
		for _, v := range views {
			if at := v.freeAt(now); at != nil && (first == nil || at.Before(*first)) {
				first, firstName = at, v.meta.Name
			}
		}
		hint := ""
		if first != nil {
			hint = fmt.Sprintf("'%s' frees up in %s (%s)", firstName, fmtDur(first.Sub(now)), fmtWhen(*first, now))
		}
		return nil, "", &runtimeError{msg, hint}
	}
	if len(order) > 0 {
		return best.meta, "first with room in the order (" + where + ")", nil
	}
	why := "has room"
	if w := best.limit("seven_day"); w != nil && w.ResetsAt != nil {
		why = fmt.Sprintf("weekly resets soonest, in %s", fmtDur(w.ResetsAt.Sub(now)))
	}
	return best.meta, why, nil
}

// ----------------------------------------------------------------------------- add

func (a *app) cmdAdd(args []string) (int, error) {
	_, pos, help, err := parseFlags("add", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["add"])
		return exitOK, nil
	}
	t, name, err := a.toolAndName("add", pos, true)
	if err != nil {
		return 0, err
	}
	st, err := a.loadState(t)
	if err != nil {
		return 0, err
	}
	if findAccount(st.accounts, name) != nil {
		return 0, &runtimeError{fmt.Sprintf("%s account '%s' already exists", t.name, name), "pick another name, or 'arl remove' it first"}
	}
	stamp := a.now().UTC().Format(time.RFC3339)

	// The current login is not tracked yet: register it as this account.
	if st.hasLive && st.active == nil {
		if st.live.ID == "" {
			return 0, &runtimeError{fmt.Sprintf("could not tell which account the current %s login belongs to", t.name),
				fmt.Sprintf("run %s once so it records the account, then retry", t.bin)}
		}
		m := &meta{identity: st.live, Name: name, AddedAt: stamp}
		if t == claudeTool {
			m.OAuthAccount = a.liveOAuthAccount()
		}
		if err := a.writeMeta(t, m); err != nil {
			return 0, err
		}
		a.saveActive()
		fmt.Fprintf(a.stdout, "%s: registered the current login (%s) as '%s', active\n", t.name, describe(m.identity), name)
		return exitOK, nil
	}

	secret, oa, err := a.loginNew(t)
	if err != nil {
		return 0, err
	}
	var id identity
	if t == claudeTool {
		id = claudeIdentity(secret, oa)
	} else {
		id = codexIdentity(secret)
	}
	if dup := findByID(st.accounts, id.ID); dup != nil {
		return 0, &runtimeError{fmt.Sprintf("that login (%s) is already account '%s'", describe(id), dup.Name), "sign in with a different account to add it"}
	}
	m := &meta{identity: id, Name: name, AddedAt: stamp, OAuthAccount: oa}
	if !st.hasLive {
		if err := a.setLiveSecret(t, secret); err != nil {
			return 0, err
		}
		if t == claudeTool {
			_ = a.setLiveOAuthAccount(oa)
		}
	} else if err := a.setVaultSecret(t, name, secret); err != nil {
		return 0, err
	}
	if err := a.writeMeta(t, m); err != nil {
		return 0, err
	}
	a.saveActive()
	if !st.hasLive {
		fmt.Fprintf(a.stdout, "%s: added '%s' (%s), active\n", t.name, name, describe(id))
	} else {
		fmt.Fprintf(a.stdout, "%s: added '%s' (%s)\n", t.name, name, describe(id))
		fmt.Fprintf(a.stderr, "  use it with: arl switch %s %s\n", t.name, name)
	}
	return exitOK, nil
}

func describe(id identity) string {
	parts := []string{}
	if id.Email != "" {
		parts = append(parts, id.Email)
	}
	if id.Plan != "" {
		parts = append(parts, id.Plan)
	}
	if len(parts) == 0 {
		return "unknown account"
	}
	return strings.Join(parts, ", ")
}

// cleanEnv drops variables that would send the CLI somewhere other than its normal login.
func cleanEnv(extra ...string) []string {
	drop := map[string]bool{"ANTHROPIC_BASE_URL": true, "ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_API_KEY": true,
		"CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true}
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] && !strings.HasPrefix(k, "CLAUDE_CODE_") && k != "CLAUDECODE" {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

// loginNew signs a new account in inside a throwaway config dir and returns its login.
func (a *app) loginNew(t *tool) ([]byte, json.RawMessage, error) {
	bin, err := a.lookPath(t.bin)
	if err != nil {
		return nil, nil, &runtimeError{fmt.Sprintf("'%s' not found on PATH", t.bin), t.installHint}
	}
	tmp := filepath.Join(a.dataDir(), "login-"+randomSuffix())
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)

	if t == codexTool {
		fmt.Fprintln(a.stderr, "Signing in a new Codex account (pick the account you want to add)...")
		argv := []string{t.bin, "login", "-c", `cli_auth_credentials_store="file"`}
		if err := a.runInteractive(bin, argv, cleanEnv("CODEX_HOME="+tmp)); err != nil {
			return nil, nil, &runtimeError{"codex login failed: " + err.Error(), ""}
		}
		secret, err := readOptional(filepath.Join(tmp, "auth.json"))
		if err != nil || len(secret) == 0 {
			return nil, nil, &runtimeError{"codex login did not produce a login", ""}
		}
		return secret, nil, nil
	}

	var before map[string]bool
	if a.goos == "darwin" {
		before = a.kcServices(liveKeychainService + "-")
	}
	fmt.Fprintln(a.stderr, "Signing in a new Claude account (pick the account you want to add)...")
	if err := a.runInteractive(bin, []string{t.bin, "auth", "login"}, cleanEnv("CLAUDE_CONFIG_DIR="+tmp)); err != nil {
		return nil, nil, &runtimeError{"claude auth login failed: " + err.Error(), ""}
	}
	var secret []byte
	if a.goos == "darwin" {
		var fresh []string
		for svc := range a.kcServices(liveKeychainService + "-") {
			if !before[svc] {
				fresh = append(fresh, svc)
			}
		}
		if len(fresh) != 1 {
			return nil, nil, &runtimeError{fmt.Sprintf("could not find the new login in the Keychain (%d candidates)", len(fresh)), ""}
		}
		if secret, err = a.kcRead(fresh[0]); err != nil {
			return nil, nil, err
		}
		_ = a.kcDelete(fresh[0])
	} else if secret, err = readOptional(filepath.Join(tmp, ".credentials.json")); err != nil {
		return nil, nil, err
	}
	if len(secret) == 0 {
		return nil, nil, &runtimeError{"claude auth login did not produce a login", ""}
	}
	var oa json.RawMessage
	if b, err := os.ReadFile(filepath.Join(tmp, ".claude.json")); err == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) == nil {
			oa = m["oauthAccount"]
		}
	}
	return secret, oa, nil
}

// ----------------------------------------------------------------------------- remove

func (a *app) cmdRemove(args []string) (int, error) {
	_, pos, help, err := parseFlags("remove", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["remove"])
		return exitOK, nil
	}
	t, name, err := a.toolAndName("remove", pos, true)
	if err != nil {
		return 0, err
	}
	st, err := a.loadState(t)
	if err != nil {
		return 0, err
	}
	m := findAccount(st.accounts, name)
	if m == nil {
		return 0, a.noSuchAccount(st, name)
	}
	active := st.active != nil && st.active.Name == name
	if !active {
		if err := a.deleteVaultSecret(t, name); err != nil {
			return 0, &runtimeError{fmt.Sprintf("could not delete the stored login: %v", err), ""}
		}
	}
	if err := os.RemoveAll(a.accountDir(t, name)); err != nil {
		return 0, err
	}
	_ = os.Remove(a.cachePath(t, name))
	a.saveActive()
	if active {
		fmt.Fprintf(a.stdout, "%s: stopped tracking '%s'; its login stays active in %s\n", t.name, name, t.title)
	} else {
		fmt.Fprintf(a.stdout, "%s: removed '%s' and its stored login\n", t.name, name)
	}
	return exitOK, nil
}

// ----------------------------------------------------------------------------- list

func (a *app) cmdList(args []string) (int, error) {
	o, pos, help, err := parseFlags("list", args, []flagSpec{{name: "--json"}, {name: "--emails"}, {name: "--no-color"}})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["list"])
		return exitOK, nil
	}
	tools, err := toolsFrom(pos)
	if err != nil {
		return 0, err
	}
	now := a.now()
	s := a.outStyle(o["--no-color"] != "")
	type row struct {
		Tool    string `json:"tool"`
		Name    string `json:"name"`
		Active  bool   `json:"active"`
		Email   string `json:"email,omitempty"`
		Plan    string `json:"plan,omitempty"`
		Session *int   `json:"session_used_pct"`
		Weekly  *int   `json:"weekly_used_pct"`
	}
	var rows []row
	var lines []string
	for _, t := range tools {
		st, err := a.loadState(t)
		if err != nil {
			return 0, err
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, s.bold(t.title))
		if len(st.accounts) == 0 {
			lines = append(lines, "  "+s.dim(fmt.Sprintf("no accounts (add one: arl add %s <name>)", t.name)))
		}
		if st.hasLive && st.active == nil {
			lines = append(lines, "  "+s.yellow("! current login "+describe(st.live)+" is not registered (arl add "+t.name+" <name>)"))
		}
		for _, v := range a.usageViews(st, false) {
			r := row{Tool: t.name, Name: v.meta.Name, Active: v.active, Plan: v.meta.Plan}
			if o["--emails"] != "" {
				r.Email = v.meta.Email
			}
			mark, name := "  ", v.meta.Name
			if v.active {
				mark, name = s.green("* "), s.green(name)
			}
			line := fmt.Sprintf("%s%s%s  %s", mark, name, strings.Repeat(" ", max(0, 10-len(v.meta.Name))), s.dim(fmt.Sprintf("%-8s", v.meta.Plan)))
			if r.Email != "" {
				line += "  " + r.Email
			}
			for _, k := range [][2]string{{"five_hour", "session"}, {"seven_day", "weekly"}} {
				if l := v.limit(k[0]); l != nil {
					p := int(l.Used*100 + 0.5)
					if k[0] == "five_hour" {
						r.Session = &p
					} else {
						r.Weekly = &p
					}
					line += fmt.Sprintf("  %s %s", k[1], colorUsed(s, l.Used, fmt.Sprintf("%d%%", p)))
				}
			}
			if v.rec != nil && !v.active {
				line += s.dim("  (as of " + fmtDur(now.Sub(v.rec.FetchedAt)) + " ago)")
			}
			lines = append(lines, line)
			rows = append(rows, r)
		}
	}
	if o["--json"] != "" {
		if rows == nil {
			rows = []row{}
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(a.stdout, string(b))
		return exitOK, nil
	}
	fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return exitOK, nil
}

func toolsFrom(pos []string) ([]*tool, error) {
	if len(pos) == 0 {
		return allTools, nil
	}
	if len(pos) > 1 {
		return nil, &usageError{"expected at most one tool (claude or codex)"}
	}
	t, err := parseTool(pos[0])
	if err != nil {
		return nil, err
	}
	return []*tool{t}, nil
}

func colorUsed(s style, used float64, text string) string {
	switch {
	case used >= 0.95:
		return s.red(text)
	case used >= 0.8:
		return s.yellow(text)
	}
	return s.green(text)
}
