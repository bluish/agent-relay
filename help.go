package main

// helpText is keyed by command; "" is the top-level help.
var helpText = map[string]string{
	"": `xorp - several Claude Code and Codex accounts on one machine, one active at a time

Usage:
  xorp <command> [args...]

Examples:
  xorp add claude work          Register the current Claude login as "work"
  xorp add claude personal      Sign in another account and keep it as "personal"
  xorp switch claude personal   Make "personal" the active Claude account
  xorp switch claude            Pick the account whose weekly limit resets soonest
  xorp usage                    Limits of every account, and when the pool runs dry
  xorp list                     Accounts per tool (no network)

Commands:
  switch <tool> [account]   Make an account the active login (no account: soonest reset)
  add <tool> <account>      Register the current login, or sign in a new account
  remove <tool> <account>   Forget an account and delete its stored login
  list [tool]               Show accounts and their last known usage
  usage [tool]              Show limits, burn rate and forecast per tool
  status                    Check logins and that nothing reroutes the CLIs
  sync                      Exchange usage readings with the hub
  serve                     Run the hub and its dashboard
  help [command]            Show help for xorp or a command

  <tool> is claude or codex.

How it works:
  Claude Code and Codex keep reading their normal login (~/.claude, the macOS
  Keychain, ~/.codex/auth.json). 'xorp switch' swaps which account that login
  is, and keeps the others in ~/.local/share/xorp (Keychain on macOS). Every
  login exists in one place only, so refresh tokens never collide. Sessions,
  settings and history are shared, so 'claude -c' or a T3 Code thread carries
  on with the new account. The first request after a switch has no prompt
  cache (caches belong to an account); later ones are cached as usual.

Options:
  -h, --help         Show this help
      --version      Show the xorp version

Hub (optional):
  One machine runs 'xorp serve'; the others put its URL in ~/.config/xorp/hub.
  Machines then share usage readings (never logins), so every machine knows
  every account's usage, and the dashboard shows all accounts and machines.

Environment:
  XORP_HUB                  Hub URL (overrides ~/.config/xorp/hub)
  XORP_MACHINE              Name this machine reports as (else ~/.config/xorp/machine,
                            else the hostname)
  NO_COLOR, XORP_NO_COLOR   Disable color
  XDG_DATA_HOME, XDG_STATE_HOME   Where accounts and usage history are kept

Exit codes:
  0 ok, 1 runtime error (or 'status' found a problem), 2 usage error

Run 'xorp help <command>' for details on a command.
`,
	"switch": `xorp switch - make an account the active login

Usage:
  xorp switch <tool> [account]

Swaps the live login of the tool (claude or codex) for the account's stored
login, and stores the previous one. Running Claude Code sessions, terminals and
T3 Code threads use the new account from their next request.

Without an account, picks soonest-reset: among accounts with room in both the
session and the weekly window, the one whose weekly limit resets first. Usage
of other accounts is the last reading taken while they were active.

Examples:
  xorp switch claude personal
  xorp switch codex work
  xorp switch claude            # work quota gone? jump to the best other account
`,
	"add": `xorp add - register an account

Usage:
  xorp add <tool> <account>

If the current login of the tool is not registered yet, it is registered under
<account> (nothing else changes). Otherwise opens the tool's sign-in for a new
account ('claude auth login' / 'codex login') in a throwaway directory and keeps
that login in the vault; the active login is untouched.

Account names: 1-32 lowercase letters, digits, '.', '_' or '-'.

Examples:
  xorp add claude work          # the login you are using now
  xorp add claude personal      # then sign in the second account
  xorp add codex personal
`,
	"remove": `xorp remove - forget an account

Usage:
  xorp remove <tool> <account>

Deletes the account's stored login and usage cache. Removing the active account
only stops tracking it; its login stays active.

Examples:
  xorp remove claude old-work
`,
	"list": `xorp list - accounts per tool

Usage:
  xorp list [tool] [--json] [--emails] [--no-color]

Shows every account, which one is active (*), its plan and its last known
session and weekly usage. Reads only local data.

Examples:
  xorp list
  xorp list claude --json
`,
	"usage": `xorp usage - limits, burn rate and forecast

Usage:
  xorp usage [tool] [--json] [--burn 1h|3h|6h] [--refresh] [--emails] [--no-color]

For each tool: the pooled weekly quota left across accounts, the burn rate,
when the pool runs dry if you always use the soonest-resetting account, and
the next reset. Then each account's session and weekly windows.

The active account is read live (at most every 4 minutes; --refresh forces it).
Other accounts show their last reading, with windows that have reset since
shown empty. The Claude Code statusline also keeps the active reading fresh.

Options:
  --json             Print JSON instead of text
  --burn WINDOW      Burn-rate window: 1h, 3h (default) or 6h
  --refresh          Read the active accounts live now
  --emails           Show account emails
  --no-color         Disable color
  -h, --help         Show this help

Examples:
  xorp usage
  xorp usage claude --burn 6h
  xorp usage --json | jq '.tools[0].forecast'
`,
	"status": `xorp status - check the setup

Usage:
  xorp status [--json] [--no-color]

Shows the active account and token state per tool, and checks that nothing
sends the CLIs elsewhere (ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN in the
environment or in ~/.claude/settings.json, a custom Codex model_provider,
CLAUDE_CONFIG_DIR / CODEX_HOME overrides). Exits 1 if a check fails.

Examples:
  xorp status
`,
	"omarchy": `xorp omarchy - update the Omarchy Agents panel

Usage:
  xorp omarchy

Writes xorp-claude.json and xorp-codex.json to ~/.local/state/omarchy/agents/usage
(one row per account and window; the active account and forecast in the
subtitle). Meant for a systemd timer.
`,
	"sync": `xorp sync - exchange usage readings with the hub

Usage:
  xorp sync

Refreshes the active accounts' usage if stale, sends this machine's accounts,
active logins and readings to the hub, and keeps any newer readings it returns.
'usage', 'switch' and 'omarchy' do this on their own; run it from a timer
(launchd, systemd) to keep the dashboard current.
`,
	"serve": `xorp serve - run the hub

Usage:
  xorp serve [--addr HOST:PORT]

Serves the dashboard at / and the hub API (POST /api/report, GET /api/state).
Listens on 127.0.0.1:8318 by default; publish it on your tailnet with
'tailscale serve --bg 8318'. There is no authentication: keep it private.
State is kept in ~/.local/state/xorp/hub.json.
`,
	"help": `xorp help - show help

Usage:
  xorp help [command]

Examples:
  xorp help
  xorp help switch
`,
}
