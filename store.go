package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Logins live in two places:
//   - the live login: where the official CLI reads it (Claude: ~/.claude/.credentials.json
//     on Linux, the "Claude Code-credentials" Keychain item on macOS, plus oauthAccount in
//     ~/.claude.json; Codex: ~/.codex/auth.json);
//   - the vault: every other registered account, under ~/.local/share/xorp/accounts/<tool>/<name>
//     (secrets in a 0600 file, or a "xorp-claude-<name>" Keychain item on macOS).
//
// A login is always in exactly one of them, never copied: OAuth refresh tokens rotate,
// so two copies of the same login would invalidate each other.

type tool struct{ name, title, bin, installHint string }

var (
	claudeTool = &tool{"claude", "Claude", "claude", "install Claude Code: https://docs.anthropic.com/en/docs/claude-code"}
	codexTool  = &tool{"codex", "Codex", "codex", "install Codex: npm install -g @openai/codex"}
	allTools   = []*tool{claudeTool, codexTool}
)

func parseTool(v string) (*tool, error) {
	s := strings.ToLower(strings.TrimSpace(v))
	for _, t := range allTools {
		if s == t.name {
			return t, nil
		}
	}
	msg := fmt.Sprintf("unknown tool '%s' (expected claude or codex)", v)
	if g := suggest(s, []string{"claude", "codex"}); g != "" {
		msg = fmt.Sprintf("unknown tool '%s'. Did you mean '%s'?", v, g)
	}
	return nil, &usageError{msg}
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

func checkName(name string) error {
	if !nameRe.MatchString(name) {
		return &usageError{fmt.Sprintf("invalid account name '%s': use 1-32 lowercase letters, digits, '.', '_' or '-'", name)}
	}
	return nil
}

// ----------------------------------------------------------------------------- paths

func (a *app) xdg(env, fallback string) string {
	if d := a.getenv(env); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "xorp")
	}
	return filepath.Join(a.home, fallback, "xorp")
}

func (a *app) dataDir() string  { return a.xdg("XDG_DATA_HOME", ".local/share") }
func (a *app) stateDir() string { return a.xdg("XDG_STATE_HOME", ".local/state") }

func (a *app) accountDir(t *tool, name string) string {
	return filepath.Join(a.dataDir(), "accounts", t.name, name)
}

func (a *app) claudeJSON() string { return filepath.Join(a.home, ".claude.json") }

func (a *app) liveFile(t *tool) string {
	if t == claudeTool {
		return filepath.Join(a.home, ".claude", ".credentials.json")
	}
	return filepath.Join(a.home, ".codex", "auth.json")
}

func (a *app) usesKeychain(t *tool) bool { return t == claudeTool && a.goos == "darwin" }

const liveKeychainService = "Claude Code-credentials"

func vaultKeychainService(name string) string { return "xorp-claude-" + name }

// ----------------------------------------------------------------------------- files

// writeFileAtomic writes via a temp file + rename so readers never see a partial file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".xorp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func readOptional(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// ----------------------------------------------------------------------------- keychain

func (a *app) kcRead(service string) ([]byte, error) {
	out, err := a.security("", "find-generic-password", "-a", a.user, "-s", service, "-w")
	if err != nil {
		if strings.Contains(err.Error(), "could not be found") || strings.Contains(err.Error(), "exit status 44") {
			return nil, nil
		}
		return nil, err
	}
	return []byte(strings.TrimRight(out, "\n")), nil
}

// kcWrite stores data through 'security -i' (stdin) so the secret never shows up in process arguments.
func (a *app) kcWrite(service string, data []byte) error {
	line := fmt.Sprintf("add-generic-password -U -a %q -s %q -X %s\n", a.user, service, hex.EncodeToString(data))
	_, err := a.security(line, "-i")
	return err
}

func (a *app) kcDelete(service string) error {
	_, err := a.security("", "delete-generic-password", "-a", a.user, "-s", service)
	if err != nil && (strings.Contains(err.Error(), "could not be found") || strings.Contains(err.Error(), "exit status 44")) {
		return nil
	}
	return err
}

// kcServices lists Keychain generic-password services starting with prefix.
func (a *app) kcServices(prefix string) map[string]bool {
	out, err := a.security("", "dump-keychain")
	found := map[string]bool{}
	if err != nil {
		return found
	}
	re := regexp.MustCompile(`"svce"<blob>="([^"]*)"`)
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		if strings.HasPrefix(m[1], prefix) {
			found[m[1]] = true
		}
	}
	return found
}

// ----------------------------------------------------------------------------- live login / vault secrets

func (a *app) liveSecret(t *tool) ([]byte, error) {
	if a.usesKeychain(t) {
		return a.kcRead(liveKeychainService)
	}
	return readOptional(a.liveFile(t))
}

func (a *app) setLiveSecret(t *tool, b []byte) error {
	if a.usesKeychain(t) {
		return a.kcWrite(liveKeychainService, b)
	}
	return writeFileAtomic(a.liveFile(t), b, 0o600)
}

func (a *app) vaultFile(t *tool, name string) string {
	file := "auth.json"
	if t == claudeTool {
		file = "credentials.json"
	}
	return filepath.Join(a.accountDir(t, name), file)
}

func (a *app) vaultSecret(t *tool, name string) ([]byte, error) {
	if a.usesKeychain(t) {
		return a.kcRead(vaultKeychainService(name))
	}
	return readOptional(a.vaultFile(t, name))
}

func (a *app) setVaultSecret(t *tool, name string, b []byte) error {
	if a.usesKeychain(t) {
		return a.kcWrite(vaultKeychainService(name), b)
	}
	return writeFileAtomic(a.vaultFile(t, name), b, 0o600)
}

func (a *app) deleteVaultSecret(t *tool, name string) error {
	if a.usesKeychain(t) {
		return a.kcDelete(vaultKeychainService(name))
	}
	err := os.Remove(a.vaultFile(t, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ----------------------------------------------------------------------------- identity

// identity says which account a login belongs to. Nothing in it is secret.
type identity struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Org   string `json:"org,omitempty"`
	Plan  string `json:"plan,omitempty"`
}

// meta is the vault's per-account record (meta.json).
type meta struct {
	identity
	Name    string `json:"name"`
	AddedAt string `json:"added_at"`
	// OAuthAccount is Claude's oauthAccount block from ~/.claude.json, restored on switch.
	OAuthAccount json.RawMessage `json:"oauth_account,omitempty"`
}

type claudeCreds struct {
	ClaudeAiOauth struct {
		AccessToken      string `json:"accessToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

type codexAuth struct {
	Tokens struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type oauthAccount struct {
	AccountUUID      string `json:"accountUuid"`
	EmailAddress     string `json:"emailAddress"`
	OrganizationUUID string `json:"organizationUuid"`
	OrganizationName string `json:"organizationName"`
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// claudeIdentity combines the oauthAccount block (who) with the credentials (plan).
func claudeIdentity(secret []byte, account json.RawMessage) identity {
	var oa oauthAccount
	_ = json.Unmarshal(account, &oa)
	id := identity{Email: oa.EmailAddress, Org: oa.OrganizationName}
	if oa.AccountUUID != "" {
		id.ID = oa.AccountUUID + ":" + oa.OrganizationUUID
	}
	var c claudeCreds
	if json.Unmarshal(secret, &c) == nil {
		id.Plan = strings.TrimSpace(c.ClaudeAiOauth.SubscriptionType)
		if tier := c.ClaudeAiOauth.RateLimitTier; strings.Contains(tier, "_max_") {
			id.Plan += " " + tier[strings.LastIndex(tier, "_")+1:]
		}
	}
	return id
}

func codexIdentity(secret []byte) identity {
	var c codexAuth
	if json.Unmarshal(secret, &c) != nil {
		return identity{}
	}
	id := identity{ID: c.Tokens.AccountID}
	if claims := jwtClaims(c.Tokens.IDToken); claims != nil {
		id.Email, _ = claims["email"].(string)
		if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			id.Plan, _ = auth["chatgpt_plan_type"].(string)
			if id.ID == "" {
				id.ID, _ = auth["chatgpt_account_id"].(string)
			}
		}
	}
	return id
}

// accessToken returns the bearer token, the extra account header (Codex) and its expiry.
func accessToken(t *tool, secret []byte) (token, accountID string, expires time.Time) {
	if t == claudeTool {
		var c claudeCreds
		if json.Unmarshal(secret, &c) == nil && c.ClaudeAiOauth.ExpiresAt > 0 {
			return c.ClaudeAiOauth.AccessToken, "", time.UnixMilli(c.ClaudeAiOauth.ExpiresAt)
		}
		return "", "", time.Time{}
	}
	var c codexAuth
	if json.Unmarshal(secret, &c) != nil {
		return "", "", time.Time{}
	}
	if claims := jwtClaims(c.Tokens.AccessToken); claims != nil {
		if exp, ok := claims["exp"].(float64); ok {
			expires = time.Unix(int64(exp), 0)
		}
	}
	return c.Tokens.AccessToken, c.Tokens.AccountID, expires
}

// liveOAuthAccount reads oauthAccount from ~/.claude.json (nil if absent).
func (a *app) liveOAuthAccount() json.RawMessage {
	b, err := os.ReadFile(a.claudeJSON())
	if err != nil {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	if v := m["oauthAccount"]; len(v) > 0 && string(v) != "null" {
		return v
	}
	return nil
}

// setLiveOAuthAccount replaces oauthAccount in ~/.claude.json, keeping every other key.
func (a *app) setLiveOAuthAccount(v json.RawMessage) error {
	path := a.claudeJSON()
	m := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %v", path, err)
		}
	}
	if len(v) == 0 {
		delete(m, "oauthAccount")
	} else {
		m["oauthAccount"] = v
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b, 0o600)
}

// liveIdentity describes the live login; ok=false when there is none.
func (a *app) liveIdentity(t *tool) (identity, []byte, bool, error) {
	secret, err := a.liveSecret(t)
	if err != nil || len(secret) == 0 {
		return identity{}, nil, false, err
	}
	if t == claudeTool {
		return claudeIdentity(secret, a.liveOAuthAccount()), secret, true, nil
	}
	return codexIdentity(secret), secret, true, nil
}

// ----------------------------------------------------------------------------- registry

func (a *app) readMeta(t *tool, name string) (*meta, error) {
	b, err := os.ReadFile(filepath.Join(a.accountDir(t, name), "meta.json"))
	if err != nil {
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m.Name = name
	return &m, nil
}

func (a *app) writeMeta(t *tool, m *meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(a.accountDir(t, m.Name), "meta.json"), b, 0o600)
}

func (a *app) accounts(t *tool) []*meta {
	entries, _ := os.ReadDir(filepath.Join(a.dataDir(), "accounts", t.name))
	var out []*meta
	for _, e := range entries {
		if !e.IsDir() || !nameRe.MatchString(e.Name()) {
			continue
		}
		if m, err := a.readMeta(t, e.Name()); err == nil {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(x, y *meta) int { return strings.Compare(x.Name, y.Name) })
	return out
}

func findAccount(list []*meta, name string) *meta {
	for _, m := range list {
		if m.Name == name {
			return m
		}
	}
	return nil
}

func findByID(list []*meta, id string) *meta {
	if id == "" {
		return nil
	}
	for _, m := range list {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// toolState is everything the commands need to know about one tool.
type toolState struct {
	tool     *tool
	accounts []*meta
	live     identity
	hasLive  bool
	active   *meta // registered account the live login belongs to (nil if none)
}

func (a *app) loadState(t *tool) (*toolState, error) {
	st := &toolState{tool: t, accounts: a.accounts(t)}
	id, _, ok, err := a.liveIdentity(t)
	if err != nil {
		return nil, &runtimeError{fmt.Sprintf("could not read the live %s login: %v", t.title, err), ""}
	}
	st.live, st.hasLive = id, ok
	if ok {
		st.active = findByID(st.accounts, id.ID)
	}
	return st, nil
}

// saveActive records the active account names for the statusline and other readers.
func (a *app) saveActive() {
	doc := map[string]string{}
	for _, t := range allTools {
		if st, err := a.loadState(t); err == nil && st.active != nil {
			doc[t.name] = st.active.Name
		}
	}
	b, _ := json.Marshal(doc)
	_ = writeFileAtomic(filepath.Join(a.stateDir(), "active.json"), b, 0o644)
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
