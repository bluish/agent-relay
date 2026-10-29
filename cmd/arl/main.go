// Command arl keeps several Claude Code / Codex subscription logins on one
// machine and switches the active one in place: the official CLIs (and apps
// built on them, such as T3 Code) keep using their normal config directory,
// and pick up the new account on their next request. No proxy is involved.
//
// It uses only the standard library so it stays small and starts fast.
// Conventions: https://clig.dev/
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var version = "2.0.0"

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
	exitNoBin = 127
)

var commands = []string{"switch", "add", "remove", "list", "usage", "history", "status", "mode", "order", "bind", "unbind", "here",
	"sync", "serve", "update", "hook", "completion", "man", "help"}

// app bundles every side effect so commands can be tested.
type app struct {
	stdout, stderr       io.Writer
	stdoutTTY, stderrTTY bool
	getenv               func(string) string
	home, user, goos     string
	now                  func() time.Time
	lookPath             func(string) (string, error)
	// runInteractive runs a command attached to the terminal (used for logins).
	runInteractive func(path string, argv, env []string) error
	// security runs macOS's security(1) with the given stdin and returns stdout.
	security func(stdin string, args ...string) (string, error)
	// countProcs counts running processes with this exact name.
	countProcs func(name string) int
	// sendNotification shows a desktop notification.
	sendNotification func(title, body string)
	// fetch does an HTTP GET and returns the status code, body and Retry-After header.
	fetch func(url string, headers map[string]string) (int, []byte, string, error)
}

func main() {
	home, _ := os.UserHomeDir()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("LOGNAME")
	}
	a := &app{
		stdout: os.Stdout, stderr: os.Stderr,
		stdoutTTY: isTerminal(os.Stdout), stderrTTY: isTerminal(os.Stderr),
		getenv: os.Getenv, home: home, user: user, goos: runtime.GOOS, now: time.Now,
		lookPath: exec.LookPath, runInteractive: runInteractive, security: runSecurity, fetch: httpGet,
		countProcs: countProcs, sendNotification: sendNotification,
	}
	a.migrateLegacy()
	os.Exit(a.run(os.Args[1:]))
}

func runInteractive(path string, argv, env []string) error {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func countProcs(name string) int {
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}

func sendNotification(title, body string) {
	if runtime.GOOS == "darwin" {
		q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
		_ = exec.Command("osascript", "-e", "display notification "+q(body)+" with title "+q(title)).Run()
		return
	}
	_ = exec.Command("notify-send", "-a", "agent-relay", title, body).Run()
}

func runSecurity(stdin string, args ...string) (string, error) {
	cmd := exec.Command("/usr/bin/security", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("security %s: %v %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return string(out), nil
}

// usageError is a bad invocation (exit 2); runtimeError is anything else that failed (exit 1).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

type runtimeError struct{ msg, hint string }

func (e *runtimeError) Error() string { return e.msg }

func (a *app) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stdout, helpText[""])
		return exitOK
	}
	cmd, rest := args[0], args[1:]
	var code int
	var err error
	switch cmd {
	case "-h", "--help":
		code, err = a.cmdHelp(nil)
	case "help":
		code, err = a.cmdHelp(rest)
	case "--version", "-V", "version":
		fmt.Fprintln(a.stdout, "arl "+version)
	case "switch", "use":
		code, err = a.cmdSwitch(rest)
	case "add":
		code, err = a.cmdAdd(rest)
	case "remove", "rm":
		code, err = a.cmdRemove(rest)
	case "list", "ls":
		code, err = a.cmdList(rest)
	case "usage":
		code, err = a.cmdUsage(rest)
	case "status":
		code, err = a.cmdStatus(rest)
	case "mode":
		code, err = a.cmdMode(rest)
	case "order":
		code, err = a.cmdOrder(rest)
	case "bind":
		code, err = a.cmdBind(rest)
	case "unbind":
		code, err = a.cmdUnbind(rest)
	case "here":
		code, err = a.cmdHere(rest)
	case "history":
		code, err = a.cmdHistory(rest)
	case "update":
		code, err = a.cmdUpdate(rest)
	case "hook":
		code, err = a.cmdHook(rest)
	case "completion":
		code, err = a.cmdCompletion(rest)
	case "man":
		code, err = a.cmdMan(rest)
	case "_accounts":
		code, err = a.cmdAccountsComplete(rest)
	case "sync":
		code, err = a.cmdSync(rest)
	case "serve":
		code, err = a.cmdServe(rest)
	case "omarchy":
		code, err = a.cmdOmarchy(rest)
	default:
		if strings.HasPrefix(cmd, "-") {
			a.errorf("run 'arl --help' for usage", "unknown option '%s'", cmd)
			return exitUsage
		}
		return a.unknownCommand(cmd)
	}
	var ue *usageError
	var re *runtimeError
	switch {
	case errors.As(err, &ue):
		a.errorf(fmt.Sprintf("run 'arl %s --help' for usage", cmd), "%s", ue.msg)
		return exitUsage
	case errors.As(err, &re):
		a.errorf(re.hint, "%s", re.msg)
		return exitErr
	case err != nil:
		a.errorf("", "%v", err)
		return exitErr
	}
	return code
}

// ----------------------------------------------------------------------------- output helpers

func colorAllowed(getenv func(string) string, tty, flagOff bool) bool {
	if flagOff || getenv("NO_COLOR") != "" || getenv("ARL_NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return false
	}
	return tty
}

type style struct{ on bool }

func (s style) wrap(code, v string) string {
	if !s.on {
		return v
	}
	return "\033[" + code + "m" + v + "\033[0m"
}
func (s style) bold(v string) string   { return s.wrap("1", v) }
func (s style) dim(v string) string    { return s.wrap("2", v) }
func (s style) red(v string) string    { return s.wrap("31", v) }
func (s style) green(v string) string  { return s.wrap("32", v) }
func (s style) yellow(v string) string { return s.wrap("33", v) }
func (s style) cyan(v string) string   { return s.wrap("36", v) }

func (a *app) outStyle(noColor bool) style {
	return style{colorAllowed(a.getenv, a.stdoutTTY, noColor)}
}
func (a *app) errStyle() style { return style{colorAllowed(a.getenv, a.stderrTTY, false)} }

func (a *app) errorf(hint, format string, args ...any) {
	fmt.Fprintf(a.stderr, "%s %s\n", a.errStyle().red("arl: error:"), fmt.Sprintf(format, args...))
	if hint != "" {
		fmt.Fprintf(a.stderr, "  %s\n", hint)
	}
}

func (a *app) note(format string, args ...any) {
	fmt.Fprintf(a.stderr, "%s %s\n", a.errStyle().yellow("arl:"), fmt.Sprintf(format, args...))
}

// ----------------------------------------------------------------------------- help

func (a *app) cmdHelp(args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(a.stdout, helpText[""])
		return exitOK, nil
	}
	if t, ok := helpText[args[0]]; ok && args[0] != "" {
		fmt.Fprint(a.stdout, t)
		return exitOK, nil
	}
	return a.unknownCommand(args[0]), nil
}

func (a *app) unknownCommand(name string) int {
	msg := fmt.Sprintf("unknown command '%s'.", name)
	if s := suggest(name, commands); s != "" {
		msg += fmt.Sprintf(" Did you mean '%s'?", s)
	}
	a.errorf("run 'arl --help' to see the commands", "%s", msg)
	return exitUsage
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// suggest returns the closest candidate within a small edit distance, or "".
func suggest(name string, candidates []string) string {
	best, bestD := "", 1<<30
	for _, c := range candidates {
		if d := levenshtein(name, c); d < bestD {
			best, bestD = c, d
		}
	}
	limit := 2
	if n := len([]rune(name)); n <= 3 {
		limit = 1
	}
	if bestD <= limit && bestD < len([]rune(name)) {
		return best
	}
	return ""
}

// ----------------------------------------------------------------------------- flag parsing

type flagSpec struct {
	name    string
	hasArg  bool
	convert func(string) (string, error)
}

// parseFlags is a tiny parser: long flags only, '--flag value' or '--flag=value',
// -h/--help anywhere. Non-flag arguments are returned in order.
func parseFlags(cmd string, args []string, specs []flagSpec) (map[string]string, []string, bool, error) {
	out := map[string]string{}
	var pos []string
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.name)
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			return out, pos, true, nil
		}
		if !strings.HasPrefix(arg, "-") {
			pos = append(pos, arg)
			continue
		}
		name, val, hasVal := strings.Cut(arg, "=")
		var spec *flagSpec
		for j := range specs {
			if specs[j].name == name {
				spec = &specs[j]
			}
		}
		if spec == nil {
			msg := fmt.Sprintf("unknown option '%s' for 'arl %s'", name, cmd)
			if s := suggest(name, names); s != "" {
				msg += fmt.Sprintf(". Did you mean '%s'?", s)
			}
			return nil, nil, false, &usageError{msg}
		}
		if !spec.hasArg {
			if hasVal {
				return nil, nil, false, &usageError{fmt.Sprintf("option '%s' takes no value", name)}
			}
			out[name] = "1"
			continue
		}
		if !hasVal {
			i++
			if i >= len(args) {
				return nil, nil, false, &usageError{fmt.Sprintf("option '%s' needs a value", name)}
			}
			val = args[i]
		}
		v := val
		if spec.convert != nil {
			var err error
			if v, err = spec.convert(val); err != nil {
				return nil, nil, false, err
			}
		}
		out[name] = v
	}
	return out, pos, false, nil
}

func burnArg(v string) (string, error) {
	s := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(v)), "h")
	if s != "1" && s != "3" && s != "6" {
		return "", &usageError{fmt.Sprintf("--burn must be 1h, 3h or 6h (got '%s')", v)}
	}
	return s, nil
}

func toolArg(v string) (string, error) {
	t, err := parseTool(v)
	if err != nil {
		return "", err
	}
	return t.name, nil
}
