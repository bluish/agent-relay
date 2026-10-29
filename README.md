# agent-relay

`arl` passes the baton between your Claude Code and Codex subscriptions.

Several Claude Code and Codex subscriptions on one machine, one active at a time.
Switch accounts in place when one runs out, see every account's limits, and know
when your quota runs dry.

```
$ arl switch claude personal
claude: switched from 'work' to 'personal'

$ arl usage
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

agent-relay does not wrap or proxy `claude` or `codex`. They keep reading their normal
login; it only changes which account that login is. Everything built on top of
them (terminals, `claude -c`, editors, [T3 Code](https://t3.codes)) carries on
with the new account from the next request.

## How it works

| | Live login (what the CLI reads) | Other accounts (the vault) |
|---|---|---|
| Claude, Linux | `~/.claude/.credentials.json` + `oauthAccount` in `~/.claude.json` | `~/.local/share/agent-relay/accounts/claude/<name>/` |
| Claude, macOS | Keychain item `Claude Code-credentials` | Keychain item `agent-relay-claude-<name>` |
| Codex | `~/.codex/auth.json` | `~/.local/share/agent-relay/accounts/codex/<name>/` |

A switch moves the live login into the vault and the target's login out of it,
with atomic writes. Each login exists in exactly one place: OAuth refresh tokens
rotate, so two copies of one login would invalidate each other. Sessions,
settings, skills and history are untouched and shared by every account.

Prompt caches belong to an account, so the first request after a switch is
uncached. Running sessions pick up the new login on their next request.

## Install

Requires Go 1.26+.

```
go install github.com/bluish/agent-relay/cmd/arl@latest
```

## Usage

```
arl add claude work          # register the login you are using now
arl add claude personal      # sign in another account (opens the browser)
arl switch claude personal   # make it active
arl switch claude            # pick the account whose weekly limit resets soonest
arl usage                    # limits, burn rate and forecast
arl list                     # accounts, no network
arl status                   # checks nothing reroutes the CLIs
```

### When an account runs out

```
arl mode claude notify 95     # desktop notification at 95% naming the next account
arl order claude personal     # accounts 'arl switch claude' and notifications may pick
eval "$(arl completion zsh)"  # completions (zsh, bash, fish)
```

`mode auto` switches by itself instead; a switch moves every session on the
machine to the new account.

`arl history` shows how much of a weekly quota each account used per day.

`arl help <command>` documents every command; `arl man` prints a man page. Output follows
[clig.dev](https://clig.dev): `--json` where it makes sense, `NO_COLOR`,
errors on stderr, exit code 2 for usage errors.

### Usage readings

The active account is read from the provider's usage endpoint (at most every
few minutes, with backoff on rate limits). Other accounts show their last
reading, with windows that have reset since shown empty. The forecast assumes
you always use the account whose weekly limit resets soonest and simulates the
pool at the current burn rate until each account's reset.

A Claude Code statusline can save the limits Claude Code already reports to
`~/.local/state/agent-relay/usage/claude-<account>.json` (same format), which keeps
readings fresh without extra requests.

## Hub (optional)

With several machines, run the hub on one of them:

```
arl serve                          # 127.0.0.1:8318, see contrib/arl-hub.service
tailscale serve --bg --https=443 --set-path=/arl 8318
```

and point the others at it:

```
echo https://<machine>.<tailnet>.ts.net/arl > ~/.config/agent-relay/hub
arl sync                           # see contrib/ for a launchd agent
```

Publish builds on the hub with `make publish`; other machines pick them up with
`arl update` (or `arl sync --update` from their timer).

Machines share usage readings, never logins or tokens. The same account signed
in on several machines counts once, so every machine knows every account's
usage, and the dashboard at `/` shows all accounts, where each one is active,
and the forecast. The hub has no authentication; keep it on a private network.

### T3 Code

The hub speaks enough of CLIProxyAPI's management API for T3 Code's
**Settings → Providers → Usage providers → Add hub**: use the hub URL and any
management key. T3 then shows every account's limits, not just the active one.

## macOS menu bar

`macos/` is a SwiftUI menu bar app (macOS 27+, Liquid Glass) that shows the
same panel: pooled quota and outlook per tool, every account's session and
weekly windows, a **Use** button per account (`arl switch`), and the dashboard
link. It reads everything through the `arl` CLI.

```
macos/build.sh --open     # builds and installs ~/Applications/Agent Relay.app
```

## Omarchy

`arl omarchy` writes the Agents panel records for [Omarchy](https://omarchy.org)
(one row per account and window, the active account and the outlook in the
subtitle). Run it from `contrib/arl-omarchy.timer`.

## Terms of service

agent-relay only moves your own logins between the places the official CLIs read them
from; requests still come from the official clients. Check your providers'
terms for how you use your subscriptions.

## License

MIT

## Disclaimer

No warranty, no support, no responsibility. This was built for one desk and
works there. In plain Portuguese, it's a *gambiarra*: it pokes fan
controllers, rewrites keyboard firmware and talks to undocumented HID
endpoints. If your fans stop, your keyboard bricks or your monitor sulks,
you get to keep both pieces. Read the code before running it as root, and if
it goes wrong anyway, well, that's a skill issue.
