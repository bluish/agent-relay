package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The hub is optional. Each machine reports its accounts and their latest usage readings
// ('arl sync'); the hub merges them by account (the same account signed in on several
// machines is one account) and hands the freshest reading of every account back. So a
// machine knows the usage of an account it has not used lately, and the dashboard
// ('arl serve') shows everything in one place. No logins or tokens ever leave a machine.

//go:embed web/index.html
var dashboardHTML []byte

const syncTimeout = 3 * time.Second

func (a *app) hubURL() string {
	if u := a.getenv("ARL_HUB"); u != "" {
		return strings.TrimRight(u, "/")
	}
	b, err := os.ReadFile(filepath.Join(a.home, ".config", "agent-relay", "hub"))
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(string(b)), "/")
}

func (a *app) machine() string {
	if m := a.getenv("ARL_MACHINE"); m != "" {
		return m
	}
	if b, err := os.ReadFile(filepath.Join(a.home, ".config", "agent-relay", "machine")); err == nil && len(bytes.TrimSpace(b)) > 0 {
		return string(bytes.TrimSpace(b))
	}
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(strings.ToLower(h), ".")
	if h == "" {
		h = "unknown"
	}
	return h
}

type hubAccount struct {
	Tool        string    `json:"tool"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Email       string    `json:"email,omitempty"`
	Plan        string    `json:"plan,omitempty"`
	Reading     *usageRec `json:"reading,omitempty"`
	ReadingFrom string    `json:"reading_from,omitempty"`
}

type hubMachine struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	SeenAt  time.Time         `json:"seen_at"`
	Active  map[string]string `json:"active"` // tool -> account id
}

type report struct {
	Machine  string            `json:"machine"`
	Version  string            `json:"version"`
	Active   map[string]string `json:"active"`
	Accounts []hubAccount      `json:"accounts"`
}

// ----------------------------------------------------------------------------- client

// collect refreshes the active accounts (per mode), syncs with the hub and returns the
// views of every tool, rebuilt from the caches when the hub brought newer readings.
func (a *app) collect(tools []*tool, mode int) (map[*tool]*toolState, map[*tool][]*accountView, error) {
	states := map[*tool]*toolState{}
	views := map[*tool][]*accountView{}
	for _, t := range tools {
		st, err := a.loadState(t)
		if err != nil {
			return nil, nil, err
		}
		states[t] = st
		views[t] = a.usageViewsMode(st, mode)
	}
	if pulled, err := a.syncHub(); err != nil {
		if mode == refreshNow {
			a.note("hub: %v", err)
		}
	} else if pulled > 0 {
		for _, t := range tools {
			fresh := a.usageViewsMode(states[t], cachedOnly)
			for i, v := range fresh {
				v.err = views[t][i].err
			}
			views[t] = fresh
		}
	}
	return states, views, nil
}

// syncHub reports every tool's accounts and stores newer readings from the hub.
// Returns how many readings were pulled; does nothing without a hub.
func (a *app) syncHub() (int, error) {
	base := a.hubURL()
	if base == "" {
		return 0, nil
	}
	rep := report{Machine: a.machine(), Version: version, Active: map[string]string{}, Accounts: []hubAccount{}}
	local := map[string]*meta{}
	for _, t := range allTools {
		st, err := a.loadState(t)
		if err != nil {
			continue
		}
		if st.active != nil {
			rep.Active[t.name] = st.active.ID
		}
		for _, m := range st.accounts {
			if m.ID == "" {
				continue
			}
			local[t.name+":"+m.ID] = m
			rep.Accounts = append(rep.Accounts, hubAccount{Tool: t.name, ID: m.ID, Name: m.Name, Email: m.Email, Plan: m.Plan, Reading: a.readCache(t, m.Name)})
		}
	}
	body, _ := json.Marshal(rep)
	req, err := http.NewRequest("POST", base+"/api/report", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: syncTimeout}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("could not reach %s: %s", base, unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("%s answered HTTP %d", base, resp.StatusCode)
	}
	var back struct {
		Accounts []hubAccount `json:"accounts"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&back); err != nil {
		return 0, fmt.Errorf("bad hub response: %v", err)
	}
	pulled := 0
	for _, ha := range back.Accounts {
		m := local[ha.Tool+":"+ha.ID]
		t, _ := parseTool(ha.Tool)
		if m == nil || t == nil || ha.Reading == nil {
			continue
		}
		if cur := a.readCache(t, m.Name); cur == nil || ha.Reading.FetchedAt.After(cur.FetchedAt) {
			a.writeCache(t, m.Name, ha.Reading)
			pulled++
		}
	}
	return pulled, nil
}

func (a *app) cmdSync(args []string) (int, error) {
	_, pos, help, err := parseFlags("sync", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["sync"])
		return exitOK, nil
	}
	if len(pos) > 0 {
		return 0, &usageError{fmt.Sprintf("'arl sync' takes no arguments (got '%s')", pos[0])}
	}
	if a.hubURL() == "" {
		return 0, &runtimeError{"no hub configured", "put its URL in ~/.config/agent-relay/hub (or set ARL_HUB)"}
	}
	for _, t := range allTools {
		if st, err := a.loadState(t); err == nil {
			a.usageViewsMode(st, refreshStale)
		}
	}
	pulled, err := a.syncHub()
	if err != nil {
		return 0, &runtimeError{"sync failed: " + err.Error(), ""}
	}
	fmt.Fprintf(a.stdout, "synced with %s as '%s' (%d newer reading%s)\n", a.hubURL(), a.machine(), pulled, map[bool]string{true: "", false: "s"}[pulled == 1])
	return exitOK, nil
}

// ----------------------------------------------------------------------------- server

type hubServer struct {
	a        *app
	mu       sync.Mutex
	path     string
	Accounts map[string]*hubAccount `json:"accounts"` // tool:id
	Machines map[string]*hubMachine `json:"machines"`
}

func (a *app) cmdServe(args []string) (int, error) {
	o, pos, help, err := parseFlags("serve", args, []flagSpec{{name: "--addr", hasArg: true}})
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["serve"])
		return exitOK, nil
	}
	if len(pos) > 0 {
		return 0, &usageError{fmt.Sprintf("'arl serve' takes no arguments (got '%s')", pos[0])}
	}
	addr := orDefault(o["--addr"], "127.0.0.1:8318")
	h := &hubServer{a: a, path: filepath.Join(a.stateDir(), "hub.json"), Accounts: map[string]*hubAccount{}, Machines: map[string]*hubMachine{}}
	if b, err := os.ReadFile(h.path); err == nil {
		_ = json.Unmarshal(b, h)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboardHTML)
	})
	mux.HandleFunc("GET /api/state", h.handleState)
	mux.HandleFunc("GET /api/theme", h.handleTheme)
	mux.HandleFunc("POST /api/report", h.handleReport)
	fmt.Fprintf(a.stderr, "arl hub listening on http://%s\n", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		return 0, &runtimeError{err.Error(), ""}
	}
	return exitOK, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func (h *hubServer) handleReport(w http.ResponseWriter, r *http.Request) {
	var rep report
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&rep); err != nil || rep.Machine == "" {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	now := h.a.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Machines[rep.Machine] = &hubMachine{Name: rep.Machine, Version: rep.Version, SeenAt: now.UTC(), Active: rep.Active}
	touched := map[string]bool{}
	for _, in := range rep.Accounts {
		if in.ID == "" || (in.Tool != "claude" && in.Tool != "codex") {
			continue
		}
		key := in.Tool + ":" + in.ID
		cur := h.Accounts[key]
		if cur == nil {
			cur = &hubAccount{Tool: in.Tool, ID: in.ID}
			h.Accounts[key] = cur
		}
		cur.Name, cur.Email, cur.Plan = in.Name, in.Email, in.Plan
		if in.Reading != nil && (cur.Reading == nil || in.Reading.FetchedAt.After(cur.Reading.FetchedAt)) {
			cur.Reading, cur.ReadingFrom = in.Reading, rep.Machine
			touched[in.Tool] = true
		}
	}
	for _, t := range allTools {
		if touched[t.name] {
			appendSample(h.historyPath(t), h.views(t, now), now)
		}
	}
	if b, err := json.MarshalIndent(h, "", "  "); err == nil {
		_ = writeFileAtomic(h.path, b, 0o600)
	}
	var out []hubAccount
	for _, in := range rep.Accounts {
		if cur := h.Accounts[in.Tool+":"+in.ID]; cur != nil {
			out = append(out, *cur)
		}
	}
	writeJSON(w, map[string]any{"accounts": out})
}

func (h *hubServer) historyPath(t *tool) string {
	return filepath.Join(h.a.stateDir(), "hub-history-"+t.name+".jsonl")
}

// views turns the merged accounts of a tool into account views (caller holds mu).
func (h *hubServer) views(t *tool, now time.Time) []*accountView {
	var out []*accountView
	for _, ha := range h.Accounts {
		if ha.Tool != t.name {
			continue
		}
		v := &accountView{meta: &meta{Name: ha.Name, identity: identity{ID: ha.ID, Email: ha.Email, Plan: ha.Plan}}, rec: ha.Reading}
		if ha.Reading != nil {
			v.limits = effective(ha.Reading.Limits, now)
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(x, y *accountView) int { return strings.Compare(x.meta.Name, y.meta.Name) })
	return out
}

func (h *hubServer) handleState(w http.ResponseWriter, r *http.Request) {
	now := h.a.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	type acct struct {
		Name        string     `json:"name"`
		Plan        string     `json:"plan,omitempty"`
		ActiveOn    []string   `json:"active_on"`
		FetchedAt   *time.Time `json:"fetched_at,omitempty"`
		ReadingFrom string     `json:"reading_from,omitempty"`
		Limits      []limitRec `json:"limits"`
		AtLimit     bool       `json:"at_limit"`
		FreeAt      *time.Time `json:"free_at,omitempty"`
	}
	type toolOut struct {
		Tool     string   `json:"tool"`
		Title    string   `json:"title"`
		Forecast forecast `json:"forecast"`
		Outlook  string   `json:"outlook"`
		Accounts []acct   `json:"accounts"`
	}
	var tools []toolOut
	for _, t := range allTools {
		views := h.views(t, now)
		if len(views) == 0 {
			continue
		}
		fc := computeForecast(views, readSamples(h.historyPath(t), now.Add(-3*time.Hour)), 3, now)
		to := toolOut{Tool: t.name, Title: t.title, Forecast: fc, Outlook: fc.outlook(func(x time.Time) string { return x.UTC().Format(time.RFC3339) })}
		for _, v := range views {
			ac := acct{Name: v.meta.Name, Plan: v.meta.Plan, ActiveOn: []string{}, Limits: v.limits, AtLimit: !v.available(now), FreeAt: v.freeAt(now)}
			if ac.Limits == nil {
				ac.Limits = []limitRec{}
			}
			if v.rec != nil {
				ac.FetchedAt = &v.rec.FetchedAt
				ac.ReadingFrom = h.Accounts[t.name+":"+v.meta.ID].ReadingFrom
			}
			for _, m := range h.Machines {
				if m.Active[t.name] == v.meta.ID {
					ac.ActiveOn = append(ac.ActiveOn, m.Name)
				}
			}
			slices.Sort(ac.ActiveOn)
			to.Accounts = append(to.Accounts, ac)
		}
		tools = append(tools, to)
	}
	machines := make([]*hubMachine, 0, len(h.Machines))
	for _, m := range h.Machines {
		machines = append(machines, m)
	}
	slices.SortFunc(machines, func(x, y *hubMachine) int { return strings.Compare(x.Name, y.Name) })
	if tools == nil {
		tools = []toolOut{}
	}
	writeJSON(w, map[string]any{"generated_at": now.UTC(), "tools": tools, "machines": machines})
}

// handleTheme serves the current Omarchy theme colors (empty object elsewhere).
func (h *hubServer) handleTheme(w http.ResponseWriter, r *http.Request) {
	for _, p := range []string{
		filepath.Join(h.a.home, ".local/share/omarchy-webapps-theme/colors.json"),
		filepath.Join(h.a.home, ".config/omarchy/current/theme/colors.json"),
	} {
		if b, err := os.ReadFile(p); err == nil && json.Valid(b) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b)
			return
		}
	}
	writeJSON(w, map[string]any{})
}
