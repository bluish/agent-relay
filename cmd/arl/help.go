package main

// helpText is keyed by command; "" is the top-level help.
var helpText = map[string]string{
	"": `arl (agent-relay) - several Claude Code and Codex accounts on one machine, one active at a time

Usage:
  arl <command> [args...]

Examples:
  arl add claude work          Register the current Claude login as "work"
  arl add claude personal      Sign in another account and keep it as "personal"
  arl switch claude personal   Make "personal" the active Claude account
  arl switch claude            Pick the next account (configured order, else soonest reset)
  arl mode claude notify 95    Get notified when the active account hits 95%
  arl order claude personal    Only ever pick "personal" automatically
  arl usage                    Limits of every account, and when the pool runs dry
  arl history                  Quota used per day this week

Commands:
  switch <tool> [account]   Make an account the active login (no account: pick one)
  add <tool> <account>      Register the current login, or sign in a new account
  remove <tool> <account>   Forget an account and delete its stored login
  list [tool]               Show accounts and their last known usage
  usage [tool]              Show limits, burn rate and forecast per tool
  history [tool]            Show how much quota each account used per day
  status                    Check logins and that nothing reroutes the CLIs

  mode [tool] [mode] [n]    manual, notify or auto-switch at n% (default 95)
  order <tool> <account...> Accounts 'switch <tool>' and notifications pick, in order

  sync                      Exchange usage readings with the hub
  serve                     Run the hub and its dashboard
  update                    Replace arl with the hub's build
  completion zsh|bash|fish  Completions only
  man                       Print the man page
  help [command]            Show help for arl or a command

  <tool> is claude or codex.

How it works:
  Claude Code and Codex keep reading their normal login (~/.claude, the macOS
  Keychain, ~/.codex/auth.json). 'arl switch' swaps which account that login
  is, and keeps the others in ~/.local/share/agent-relay (Keychain on macOS). Every
  login exists in one place only, so refresh tokens never collide. Sessions,
  settings and history are shared, so 'claude -c' or a T3 Code thread carries
  on with the new account. The first request after a switch has no prompt
  cache (caches belong to an account); later ones are cached as usual.

Options:
  -h, --help         Show this help
      --version      Show the arl version

Hub (optional):
  One machine runs 'arl serve'; the others put its URL in ~/.config/agent-relay/hub.
  Machines then share usage readings (never logins), so every machine knows
  every account's usage, and the dashboard shows all accounts and machines.

Environment:
  ARL_HUB                  Hub URL (overrides ~/.config/agent-relay/hub)
  ARL_MACHINE              Name this machine reports as (else ~/.config/agent-relay/machine,
                            else the hostname)
  NO_COLOR, ARL_NO_COLOR   Disable color
  XDG_DATA_HOME, XDG_STATE_HOME   Where accounts and usage history are kept

Exit codes:
  0 ok, 1 runtime error (or 'status' found a problem), 2 usage error

Run 'arl help <command>' for details on a command.
`,
	"switch": `arl switch - make an account the active login

Usage:
  arl switch <tool> [account]

Swaps the live login of the tool (claude or codex) for the account's stored
login, and stores the previous one. Running Claude Code sessions, terminals and
T3 Code threads use the new account from their next request.

Without an account, picks soonest-reset: among accounts with room in both the
session and the weekly window, the one whose weekly limit resets first. Usage
of other accounts is the last reading taken while they were active.

Examples:
  arl switch claude personal
  arl switch codex work
  arl switch claude            # work quota gone? jump to the best other account
`,
	"add": `arl add - register an account

Usage:
  arl add <tool> <account>

If the current login of the tool is not registered yet, it is registered under
<account> (nothing else changes). Otherwise opens the tool's sign-in for a new
account ('claude auth login' / 'codex login') in a throwaway directory and keeps
that login in the vault; the active login is untouched.

Account names: 1-32 lowercase letters, digits, '.', '_' or '-'.

Examples:
  arl add claude work          # the login you are using now
  arl add claude personal      # then sign in the second account
  arl add codex personal
`,
	"remove": `arl remove - forget an account

Usage:
  arl remove <tool> <account>

Deletes the account's stored login and usage cache. Removing the active account
only stops tracking it; its login stays active.

Examples:
  arl remove claude old-work
`,
	"list": `arl list - accounts per tool

Usage:
  arl list [tool] [--json] [--emails] [--no-color]

Shows every account, which one is active (*), its plan and its last known
session and weekly usage. Reads only local data.

Examples:
  arl list
  arl list claude --json
`,
	"usage": `arl usage - limits, burn rate and forecast

Usage:
  arl usage [tool] [--json] [--burn 1h|3h|6h] [--refresh] [--emails] [--no-color]

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
  arl usage
  arl usage claude --burn 6h
  arl usage --json | jq '.tools[0].forecast'
`,
	"status": `arl status - check the setup

Usage:
  arl status [--json] [--no-color]

Shows the active account and token state per tool, and checks that nothing
sends the CLIs elsewhere (ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN in the
environment or in ~/.claude/settings.json, a custom Codex model_provider,
CLAUDE_CONFIG_DIR / CODEX_HOME overrides). Exits 1 if a check fails.

Examples:
  arl status
`,
	"omarchy": `arl omarchy - update the Omarchy Agents panel

Usage:
  arl omarchy

Writes arl-claude.json and arl-codex.json to ~/.local/state/omarchy/agents/usage
(one row per account and window; the active account and forecast in the
subtitle). Meant for a systemd timer.
`,
	"sync": `arl sync - exchange usage readings with the hub

Usage:
  arl sync [--update] [--quiet]

Refreshes the active accounts' usage if stale, sends this machine's accounts,
active logins and readings to the hub, and keeps any newer readings it returns.
'usage', 'switch' and 'omarchy' do this on their own; run it from a timer
(launchd, systemd) to keep the dashboard current; it also applies 'arl mode'.
--update installs a newer build from the hub (see 'arl update').
`,
	"serve": `arl serve - run the hub

Usage:
  arl serve [--addr HOST:PORT]

Serves the dashboard at / and the hub API (POST /api/report, GET /api/state).
Listens on 127.0.0.1:8318 by default; publish it on your tailnet with
'tailscale serve --bg --set-path=/arl 8318'. There is no authentication: keep it private.
State is kept in ~/.local/state/agent-relay/hub.json.
`,
	"mode": `arl mode - act when the active account nears its limit

Usage:
  arl mode [tool] [manual|notify|auto] [threshold]

When the active account's session or weekly usage reaches the threshold
(default 95%), 'notify' shows a desktop notification naming the next account,
and 'auto' switches to it (the first with room in the default order, see
'arl order'; without one, the soonest to reset). Running sessions continue on
the new account. Checked by every background 'arl sync' / 'arl omarchy'.

Examples:
  arl mode                       # show the settings
  arl mode claude auto 95
  arl mode codex notify
`,
	"order": `arl order - which accounts to pick, in order

Usage:
  arl order [tool] [account...]
  arl order <tool> --clear

The accounts 'arl switch <tool>' (without a name), notifications and auto mode
may pick; the first one with room wins. Without an order every account is
allowed, soonest reset first.

Examples:
  arl order claude personal      # never suggest the work account
  arl order                      # show
`,
	"history": `arl history - quota used per day

Usage:
  arl history [tool] [--days N] [--json] [--no-color]

For each account, how much of a weekly quota it used each day (7 days by
default), from the hub's history when a hub is set, else this machine's.
A seventh of the quota per day is on pace: more shows yellow, double red.
`,
	"update": `arl update - update arl from the hub

Usage:
  arl update [--check]

Downloads the hub's build for this OS and architecture and replaces this
executable when it differs (checked by SHA-256). Publish builds on the hub
with 'make publish'. 'arl sync --update' does the same quietly.
`,
	"completion": `arl completion - shell completions

Usage:
  arl completion zsh|bash|fish

Prints completions for commands, tools, account names and shells. Add to your
shell config:
  zsh:  eval "$(arl completion zsh)"
  bash: eval "$(arl completion bash)"
  fish: arl completion fish | source
`,
	"man": `arl man - print the man page

Usage:
  arl man | man -l -          (Linux)
  arl man | mandoc | less     (macOS)
`,
	"help": `arl help - show help

Usage:
  arl help [command]

Examples:
  arl help
  arl help switch
`,
}
