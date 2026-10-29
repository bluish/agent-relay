# xorp

Several Claude Code and Codex subscriptions on one machine, one active at a time.
Switch accounts in place when one runs out, see every account's limits, and know
when your quota runs dry.

```
$ xorp switch claude personal
claude: switched from 'work' to 'personal'

$ xorp usage
Claude  195% of 200% left · burn 0.1%/h · lasts until the reset (Tue 20:00, +22%)
  personal  max 5x  ACTIVE
    Session  ░░░░░░░░░░   5% used · resets in 3h 45m
    Weekly   ░░░░░░░░░░   5% used · resets in 5d 10h
  work  team
    Session  ░░░░░░░░░░   0% used · resets in 4h 35m
    Weekly   ░░░░░░░░░░   0% used · resets in 6d 15h
```

## Why

Work account with a small quota, personal account with a bigger one. You want to
use the work quota first and move to personal when it is gone, without logging
out, without separate config directories, and without routing anything through
a proxy.

xorp does not wrap or proxy `claude` or `codex`. They keep reading their normal
login; xorp only changes which account that login is. Everything built on top of
them (terminals, `claude -c`, editors, [T3 Code](https://t3.codes)) carries on
with the new account from the next request.

## How it works

| | Live login (what the CLI reads) | Other accounts (the vault) |
|---|---|---|
| Claude, Linux | `~/.claude/.credentials.json` + `oauthAccount` in `~/.claude.json` | `~/.local/share/xorp/accounts/claude/<name>/` |
| Claude, macOS | Keychain item `Claude Code-credentials` | Keychain item `xorp-claude-<name>` |
| Codex | `~/.codex/auth.json` | `~/.local/share/xorp/accounts/codex/<name>/` |

A switch moves the live login into the vault and the target's login out of it,
with atomic writes. Each login exists in exactly one place: OAuth refresh tokens
rotate, so two copies of one login would invalidate each other. Sessions,
settings, skills and history are untouched and shared by every account.

Prompt caches belong to an account, so the first request after a switch is
uncached. Running sessions pick up the new login on their next request.

## Install

Requires Go 1.26+.

```
go install github.com/bluish/xorp@latest
```

## Usage

```
xorp add claude work          # register the login you are using now
xorp add claude personal      # sign in another account (opens the browser)
xorp switch claude personal   # make it active
xorp switch claude            # pick the account whose weekly limit resets soonest
xorp usage                    # limits, burn rate and forecast
xorp list                     # accounts, no network
xorp status                   # checks nothing reroutes the CLIs
```

`xorp help <command>` documents every command. Output follows
[clig.dev](https://clig.dev): `--json` where it makes sense, `NO_COLOR`,
errors on stderr, exit code 2 for usage errors.

### Usage readings

The active account is read from the provider's usage endpoint (at most every
few minutes, with backoff on rate limits). Other accounts show their last
reading, with windows that have reset since shown empty. The forecast assumes
you always use the account whose weekly limit resets soonest and simulates the
pool at the current burn rate until each account's reset.

A Claude Code statusline can save the limits Claude Code already reports to
`~/.local/state/xorp/usage/claude-<account>.json` (same format), which keeps
readings fresh without extra requests.

## Hub (optional)

With several machines, run the hub on one of them:

```
xorp serve                          # 127.0.0.1:8318, see contrib/xorp-hub.service
tailscale serve --bg --https=443 8318
```

and point the others at it:

```
echo https://<machine>.<tailnet>.ts.net > ~/.config/xorp/hub
xorp sync                           # see contrib/ for a launchd agent
```

Machines share usage readings, never logins or tokens. The same account signed
in on several machines counts once, so every machine knows every account's
usage, and the dashboard at `/` shows all accounts, where each one is active,
and the forecast. The hub has no authentication; keep it on a private network.

## Omarchy

`xorp omarchy` writes the Agents panel records for [Omarchy](https://omarchy.org)
(one row per account and window, the active account and the outlook in the
subtitle). Run it from `contrib/xorp-omarchy.timer`.

## Terms of service

xorp only moves your own logins between the places the official CLIs read them
from; requests still come from the official clients. Check your providers'
terms for how you use your subscriptions.

## License

MIT
