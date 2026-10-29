package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Settings live in ~/.config/agent-relay/config, one 'tool.key = value' per line:
//
//	claude.mode = auto        # manual | notify | auto
//	claude.threshold = 95     # percent of a window that counts as "at the limit"
//	claude.order = personal   # accounts 'arl switch' and notifications may pick, in order
//

const (
	modeManual = "manual"
	modeNotify = "notify"
	modeAuto   = "auto"

	defaultThreshold = 95
)

type toolConfig struct {
	Mode      string
	Threshold int
	Order     []string
}

type settings map[string]*toolConfig

func (a *app) configPath() string { return filepath.Join(a.home, ".config", appDir, "config") }

func (a *app) loadSettings() settings {
	s := settings{}
	for _, t := range allTools {
		s[t.name] = &toolConfig{Mode: modeManual, Threshold: defaultThreshold}
	}
	b, err := os.ReadFile(a.configPath())
	if err != nil {
		return s
	}
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		toolName, key, ok := strings.Cut(strings.TrimSpace(k), ".")
		c := s[toolName]
		if !ok || c == nil {
			continue
		}
		v = strings.TrimSpace(v)
		switch key {
		case "mode":
			if v == modeManual || v == modeNotify || v == modeAuto {
				c.Mode = v
			}
		case "threshold":
			if n, err := strconv.Atoi(v); err == nil && n >= 50 && n <= 100 {
				c.Threshold = n
			}
		case "order":
			c.Order = strings.Fields(v)
		}
	}
	return s
}

func (a *app) saveSettings(s settings) error {
	var b strings.Builder
	b.WriteString("# agent-relay settings (see 'arl help mode' and 'arl help order')\n")
	names := make([]string, 0, len(s))
	for n := range s {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := s[n]
		fmt.Fprintf(&b, "%s.mode = %s\n%s.threshold = %d\n", n, c.Mode, n, c.Threshold)
		if len(c.Order) > 0 {
			fmt.Fprintf(&b, "%s.order = %s\n", n, strings.Join(c.Order, " "))
		}
	}
	return writeFileAtomic(a.configPath(), []byte(b.String()), 0o644)
}

// limitFrac is the usage fraction at which an account no longer counts as available.
func (c *toolConfig) limitFrac() float64 {
	if c.Mode == modeManual {
		return 0.999
	}
	return float64(c.Threshold) / 100
}

// orderFor is the configured account order, or nil (every account, soonest reset
// first). where names the file it came from.
func (a *app) orderFor(t *tool, s settings) (order []string, where string) {
	if o := s[t.name].Order; len(o) > 0 {
		return o, a.configPath()
	}
	return nil, ""
}

// ----------------------------------------------------------------------------- choosing

func (v *accountView) availableUnder(limit float64) bool {
	for _, k := range []string{"five_hour", "seven_day"} {
		if l := v.limit(k); l != nil && l.Used >= limit {
			return false
		}
	}
	return true
}

// choose picks the account to use: the first available one in order, or with no order
// the available account whose weekly limit resets soonest. skip is never chosen.
func choose(views []*accountView, order []string, limit float64, skip string) *accountView {
	if len(order) > 0 {
		for _, name := range order {
			for _, v := range views {
				if v.meta.Name == name && name != skip && v.availableUnder(limit) {
					return v
				}
			}
		}
		return nil
	}
	var best *accountView
	for _, v := range views {
		if v.meta.Name == skip || !v.availableUnder(limit) {
			continue
		}
		if best == nil || v.betterThan(best) {
			best = v
		}
	}
	return best
}

// ----------------------------------------------------------------------------- mode / order

func (a *app) cmdMode(args []string) (int, error) {
	_, pos, help, err := parseFlags("mode", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["mode"])
		return exitOK, nil
	}
	s := a.loadSettings()
	if len(pos) == 0 {
		for _, t := range allTools {
			c := s[t.name]
			fmt.Fprintf(a.stdout, "%-7s %-7s threshold %d%%\n", t.name, c.Mode, c.Threshold)
		}
		return exitOK, nil
	}
	t, err := parseTool(pos[0])
	if err != nil {
		return 0, err
	}
	c := s[t.name]
	if len(pos) > 1 {
		switch pos[1] {
		case modeManual, modeNotify, modeAuto:
			c.Mode = pos[1]
		default:
			return 0, &usageError{fmt.Sprintf("mode must be manual, notify or auto (got '%s')", pos[1])}
		}
	}
	if len(pos) > 2 {
		n, err := strconv.Atoi(strings.TrimSuffix(pos[2], "%"))
		if err != nil || n < 50 || n > 100 {
			return 0, &usageError{fmt.Sprintf("threshold must be a percentage between 50 and 100 (got '%s')", pos[2])}
		}
		c.Threshold = n
	}
	if len(pos) > 3 {
		return 0, &usageError{"too many arguments for 'arl mode'"}
	}
	if len(pos) > 1 {
		if err := a.saveSettings(s); err != nil {
			return 0, err
		}
	}
	fmt.Fprintf(a.stdout, "%s: %s at %d%%\n", t.name, c.Mode, c.Threshold)
	return exitOK, nil
}

func (a *app) checkAccounts(t *tool, names []string) error {
	known := accountNames(a.accounts(t))
	for _, n := range names {
		if err := checkName(n); err != nil {
			return err
		}
		found := false
		for _, k := range known {
			found = found || k == n
		}
		if !found {
			msg := fmt.Sprintf("no %s account named '%s'", t.name, n)
			if s := suggest(n, known); s != "" {
				msg += fmt.Sprintf(". Did you mean '%s'?", s)
			}
			return &runtimeError{msg, "see 'arl list'"}
		}
	}
	return nil
}

func (a *app) cmdOrder(args []string) (int, error) {
	o, pos, help, err := parseFlags("order", args, []flagSpec{{name: "--clear"}})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["order"])
		return exitOK, nil
	}
	s := a.loadSettings()
	if len(pos) == 0 {
		for _, t := range allTools {
			order := strings.Join(s[t.name].Order, " ")
			if order == "" {
				order = "(any account, soonest reset first)"
			}
			fmt.Fprintf(a.stdout, "%-7s %s\n", t.name, order)
		}
		return exitOK, nil
	}
	t, err := parseTool(pos[0])
	if err != nil {
		return 0, err
	}
	switch {
	case o["--clear"] != "":
		s[t.name].Order = nil
	case len(pos) == 1:
		fmt.Fprintf(a.stdout, "%s\n", strings.Join(s[t.name].Order, " "))
		return exitOK, nil
	default:
		if err := a.checkAccounts(t, pos[1:]); err != nil {
			return 0, err
		}
		s[t.name].Order = pos[1:]
	}
	if err := a.saveSettings(s); err != nil {
		return 0, err
	}
	if len(s[t.name].Order) == 0 {
		fmt.Fprintf(a.stdout, "%s: any account outside bound directories, soonest reset first\n", t.name)
	} else {
		fmt.Fprintf(a.stdout, "%s: outside bound directories use %s\n", t.name, strings.Join(s[t.name].Order, ", then "))
	}
	return exitOK, nil
}

// ----------------------------------------------------------------------------- auto / notify

type alertState map[string]string // tool -> "account@level" last notified

func (a *app) alertPath() string { return filepath.Join(a.stateDir(), "alerts") }

func (a *app) loadAlerts() alertState {
	out := alertState{}
	b, _ := os.ReadFile(a.alertPath())
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, " "); ok {
			out[k] = v
		}
	}
	return out
}

func (a *app) saveAlerts(s alertState) {
	var b strings.Builder
	for k, v := range s {
		fmt.Fprintf(&b, "%s %s\n", k, v)
	}
	_ = writeFileAtomic(a.alertPath(), []byte(b.String()), 0o600)
}

// watch acts on the active account crossing its threshold: a notification, or (auto)
// a switch to the next available account in the default order. Runs from background
// syncs; notifies once per account and crossing.
func (a *app) watch(states map[*tool]*toolState, views map[*tool][]*accountView) {
	s := a.loadSettings()
	alerts := a.loadAlerts()
	changed := false
	now := a.now()
	for _, t := range allTools {
		c, st := s[t.name], states[t]
		if c == nil || c.Mode == modeManual || st == nil || st.active == nil {
			continue
		}
		var active *accountView
		for _, v := range views[t] {
			if v.active {
				active = v
			}
		}
		if active == nil || active.availableUnder(c.limitFrac()) {
			if alerts[t.name] != "" {
				delete(alerts, t.name)
				changed = true
			}
			continue
		}
		key := active.meta.Name
		if alerts[t.name] == key {
			continue
		}
		alerts[t.name], changed = key, true
		used := 0.0
		for _, l := range active.limits {
			used = max(used, l.Used)
		}
		next := choose(views[t], c.Order, c.limitFrac(), active.meta.Name)
		title := fmt.Sprintf("%s '%s' at %d%%", t.title, active.meta.Name, int(used*100+0.5))
		switch {
		case next == nil:
			body := "No other account has room."
			if at := active.freeAt(now); at != nil {
				body += " It frees up " + fmtWhen(*at, now) + "."
			}
			a.notify(title, body)
		case c.Mode == modeAuto:
			if err := a.switchTo(st, next.meta); err != nil {
				a.notify(title, "Could not switch to '"+next.meta.Name+"': "+err.Error())
				continue
			}
			a.notify(fmt.Sprintf("%s switched to '%s'", t.title, next.meta.Name), fmt.Sprintf("'%s' reached %d%%. Running sessions continue on '%s'.", active.meta.Name, int(used*100+0.5), next.meta.Name))
		default:
			a.notify(title, fmt.Sprintf("Switch with: arl switch %s %s", t.name, next.meta.Name))
		}
	}
	if changed {
		a.saveAlerts(alerts)
	}
}

func (a *app) notify(title, body string) {
	if a.sendNotification != nil {
		a.sendNotification(title, body)
	}
}

// sinceDays is a helper for history windows.
func sinceDays(now time.Time, days int) time.Time {
	return now.Add(-time.Duration(days) * 24 * time.Hour)
}
