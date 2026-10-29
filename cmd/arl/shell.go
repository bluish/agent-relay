package main

import (
	"fmt"
	"strings"
	"time"
)

// commandSummaries feeds the completions and the man page.
var commandSummaries = [][2]string{
	{"switch", "make an account the active login"},
	{"add", "register the current login or sign in a new account"},
	{"remove", "forget an account"},
	{"list", "accounts and their last known usage"},
	{"usage", "limits, burn rate and forecast"},
	{"history", "quota used per day"},
	{"status", "check logins and routing"},
	{"mode", "notify when the active account nears its limit"},
	{"order", "accounts arl switch and notifications pick, in order"},
	{"sync", "exchange usage readings with the hub"},
	{"serve", "run the hub and dashboard"},
	{"update", "update arl from the hub"},
	{"completion", "shell completions"},
	{"man", "print the man page"},
	{"help", "show help"},
}

func shellArg(cmd string, pos []string) (string, error) {
	if len(pos) != 1 {
		return "", &usageError{fmt.Sprintf("usage: arl %s zsh|bash|fish", cmd)}
	}
	switch pos[0] {
	case "zsh", "bash", "fish":
		return pos[0], nil
	}
	return "", &usageError{fmt.Sprintf("unsupported shell '%s' (expected zsh, bash or fish)", pos[0])}
}

func (a *app) cmdCompletion(args []string) (int, error) {
	_, pos, help, err := parseFlags("completion", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["completion"])
		return exitOK, nil
	}
	sh, err := shellArg("completion", pos)
	if err != nil {
		return 0, err
	}
	fmt.Fprint(a.stdout, completionScript(sh))
	return exitOK, nil
}

// cmdAccountsComplete prints account names for completions (hidden).
func (a *app) cmdAccountsComplete(args []string) (int, error) {
	if len(args) != 1 {
		return exitOK, nil
	}
	if t, err := parseTool(args[0]); err == nil {
		for _, m := range a.accounts(t) {
			fmt.Fprintln(a.stdout, m.Name)
		}
	}
	return exitOK, nil
}

func completionScript(sh string) string {
	names := make([]string, 0, len(commandSummaries))
	for _, c := range commandSummaries {
		names = append(names, c[0])
	}
	switch sh {
	case "zsh":
		var desc strings.Builder
		for _, c := range commandSummaries {
			fmt.Fprintf(&desc, "    '%s:%s'\n", c[0], strings.ReplaceAll(c[1], "'", `'\''`))
		}
		return `_arl() {
  local -a cmds
  cmds=(
` + desc.String() + `  )
  if (( CURRENT == 2 )); then _describe command cmds; return; fi
  local cmd=$words[2]
  case $cmd in
    switch|remove|order)
      if (( CURRENT == 3 )); then compadd claude codex
      else compadd -- ${(f)"$(command arl _accounts $words[3])"}; fi ;;
    mode)
      case $CURRENT in 3) compadd claude codex ;; 4) compadd manual notify auto ;; esac ;;
    add|list|usage|history)
      (( CURRENT == 3 )) && compadd claude codex ;;
    completion)
      (( CURRENT == 3 )) && compadd zsh bash fish ;;
    help)
      (( CURRENT == 3 )) && _describe command cmds ;;
  esac
}
compdef _arl arl
`
	case "bash":
		return `_arl() {
  local cur=${COMP_WORDS[COMP_CWORD]} cmd=${COMP_WORDS[1]}
  if (( COMP_CWORD == 1 )); then
    COMPREPLY=($(compgen -W "` + strings.Join(names, " ") + `" -- "$cur")); return
  fi
  case $cmd in
    switch|remove|order)
      if (( COMP_CWORD == 2 )); then COMPREPLY=($(compgen -W "claude codex" -- "$cur"))
      else COMPREPLY=($(compgen -W "$(command arl _accounts "${COMP_WORDS[2]}")" -- "$cur")); fi ;;
    mode)
      case $COMP_CWORD in
        2) COMPREPLY=($(compgen -W "claude codex" -- "$cur")) ;;
        3) COMPREPLY=($(compgen -W "manual notify auto" -- "$cur")) ;;
      esac ;;
    add|list|usage|history)
      (( COMP_CWORD == 2 )) && COMPREPLY=($(compgen -W "claude codex" -- "$cur")) ;;
    completion)
      (( COMP_CWORD == 2 )) && COMPREPLY=($(compgen -W "zsh bash fish" -- "$cur")) ;;
    help)
      (( COMP_CWORD == 2 )) && COMPREPLY=($(compgen -W "` + strings.Join(names, " ") + `" -- "$cur")) ;;
  esac
}
complete -F _arl arl
`
	}
	var b strings.Builder
	b.WriteString("complete -c arl -f\n")
	for _, c := range commandSummaries {
		fmt.Fprintf(&b, "complete -c arl -n __fish_use_subcommand -a %s -d '%s'\n", c[0], strings.ReplaceAll(c[1], "'", `\'`))
	}
	b.WriteString(`complete -c arl -n "__fish_seen_subcommand_from switch remove order add list usage history mode; and test (count (commandline -opc)) -eq 2" -a "claude codex"
complete -c arl -n "__fish_seen_subcommand_from switch remove order; and test (count (commandline -opc)) -ge 3" -a "(command arl _accounts (commandline -opc)[3])"
complete -c arl -n "__fish_seen_subcommand_from mode; and test (count (commandline -opc)) -eq 3" -a "manual notify auto"
complete -c arl -n "__fish_seen_subcommand_from completion" -a "zsh bash fish"
`)
	return b.String()
}

// ----------------------------------------------------------------------------- man

func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, ".") || strings.HasPrefix(line, "'") {
			line = `\&` + line
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (a *app) cmdMan(args []string) (int, error) {
	_, _, help, err := parseFlags("man", args, nil)
	if err != nil {
		return 0, err
	}
	if help {
		fmt.Fprint(a.stdout, helpText["man"])
		return exitOK, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, ".TH ARL 1 %q \"agent-relay %s\" \"User Commands\"\n", a.now().Format(time.DateOnly), version)
	b.WriteString(".SH NAME\narl \\- switch Claude Code and Codex subscriptions in place\n")
	b.WriteString(".SH SYNOPSIS\n.B arl\n.I command\n[args...]\n")
	b.WriteString(".SH DESCRIPTION\n.nf\n" + roffEscape(helpText[""]) + "\n.fi\n")
	keys := make([]string, 0)
	for _, c := range commandSummaries {
		if _, ok := helpText[c[0]]; ok && c[0] != "help" {
			keys = append(keys, c[0])
		}
	}
	b.WriteString(".SH COMMANDS\n")
	for _, k := range keys {
		fmt.Fprintf(&b, ".SS %s\n.nf\n%s\n.fi\n", k, roffEscape(helpText[k]))
	}
	b.WriteString(".SH FILES\n.TP\n~/.config/agent-relay/config\nmode, threshold and default order per tool\n" +
		".TP\n~/.config/agent-relay/hub\nhub URL\n" +
		".TP\n~/.local/share/agent-relay\nstored logins (Keychain on macOS for Claude)\n.TP\n~/.local/state/agent-relay\nusage readings and history\n")
	fmt.Fprint(a.stdout, b.String())
	return exitOK, nil
}
