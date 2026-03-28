package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var lastExitCode int = 0
var backgroundPids = make(map[int]*exec.Cmd)
var pendingNotifs []string
var notifMu sync.Mutex

func getExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

const (
	colorReset   = "\x1b[0m"
	colorBold    = "\x1b[1m"
	colorRed     = "\x1b[31m"
	colorGreen   = "\x1b[32m"
	colorYellow  = "\x1b[33m"
	colorBlue    = "\x1b[34m"
	colorMagenta = "\x1b[35m"
	colorCyan    = "\x1b[36m"
	colorWhite   = "\x1b[37m"
)

func getGitBranch() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		headPath := filepath.Join(dir, ".git", "HEAD")
		data, err := os.ReadFile(headPath)
		if err == nil {
			content := strings.TrimSpace(string(data))
			if strings.HasPrefix(content, "ref: refs/heads/") {
				return strings.TrimPrefix(content, "ref: refs/heads/")
			}
			if len(content) >= 7 {
				return content[:7]
			}
			return content
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func getPrompt() string {
	user := os.Getenv("USER")
	host, _ := os.Hostname()
	dir, err := os.Getwd()
	if err != nil {
		dir = "?"
	}
	home := os.Getenv("HOME")
	if strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}

	symbol := "$"
	if os.Getuid() == 0 {
		symbol = "#"
	}

	prompt := fmt.Sprintf("%s%s%s@%s%s %sin %s%s%s",
		colorBold, colorCyan, user, host, colorReset,
		colorBold, colorYellow, dir, colorReset)

	branch := getGitBranch()
	if branch != "" {
		prompt += fmt.Sprintf(" %son %s%s%s",
			colorReset, colorBold, branch, colorReset)
	}

	if lastExitCode >= 1 {
		prompt += fmt.Sprintf(" %s✗%s", colorRed, colorReset)
	}

	prompt += fmt.Sprintf("\r\n%s╰%s%s ", colorBold, symbol, colorReset)

	return prompt
}

func reapZombies() {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			break
		}
		if _, ok := backgroundPids[pid]; ok {
			delete(backgroundPids, pid)
			exitCode := 0
			if status.Exited() {
				exitCode = status.ExitStatus()
			}
			notifMu.Lock()
			pendingNotifs = append(pendingNotifs, fmt.Sprintf("[%d] done (exit %d)\n", pid, exitCode))
			notifMu.Unlock()
		}
	}
}

func drainNotifs() {
	notifMu.Lock()
	msgs := pendingNotifs
	pendingNotifs = nil
	notifMu.Unlock()
	for _, m := range msgs {
		fmt.Print(m)
	}
}

func main() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		for range sigCh {
		}
	}()

	childCh := make(chan os.Signal, 1)
	signal.Notify(childCh, syscall.SIGCHLD)
	go func() {
		for range childCh {
			reapZombies()
		}
	}()

	cfg := LoadConfig()
	editor := NewLineEditor(cfg)
	for {
		reapZombies()
		drainNotifs()
		prompt := getPrompt()
		line, err := editor.ReadLine(prompt)
		if err == io.EOF {
			fmt.Println()
			break
		}
		if err != nil {
			fmt.Println()
			break
		}
		if line == "\x03" {
			lastExitCode = 130
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		chains := splitChains(line)
		for _, chain := range chains {
			executeChain(chain, cfg)
		}
	}
}

type chainEntry struct {
	operator string
	args     []string
}

func splitChains(line string) [][]chainEntry {
	var groups [][]chainEntry
	var current []chainEntry
	tokens := tokenize(line)
	i := 0
	for i < len(tokens) {
		if tokens[i] == ";" {
			if len(current) > 0 {
				groups = append(groups, current)
				current = nil
			}
			i++
			continue
		}
		var args []string
		for i < len(tokens) && tokens[i] != "&&" && tokens[i] != "||" && tokens[i] != ";" {
			args = append(args, tokens[i])
			i++
		}
		if len(args) > 0 {
			current = append(current, chainEntry{operator: "", args: args})
		}
		if i < len(tokens) && (tokens[i] == "&&" || tokens[i] == "||") {
			op := tokens[i]
			i++
			var nextArgs []string
			for i < len(tokens) && tokens[i] != "&&" && tokens[i] != "||" && tokens[i] != ";" {
				nextArgs = append(nextArgs, tokens[i])
				i++
			}
			if len(nextArgs) > 0 {
				current = append(current, chainEntry{operator: op, args: nextArgs})
			}
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func expandVariables(tokens []string) []string {
	expanded := make([]string, len(tokens))
	for i, t := range tokens {
		expanded[i] = expandString(t)
	}
	return expanded
}

func expandString(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '$' && i+1 < len(s) {
			if s[i+1] == '?' {
				result.WriteString(strconv.Itoa(lastExitCode))
				i += 2
				continue
			}
			if s[i+1] == '{' {
				end := strings.Index(s[i:], "}")
				if end >= 0 {
					varName := s[i+2 : i+end]
					val := os.Getenv(varName)
					if val == "" {
						result.WriteString("$" + s[i+1:i+end+1])
					} else {
						result.WriteString(val)
					}
					i += end + 1
					continue
				}
			}
			if s[i+1] == '$' {
				result.WriteString(strconv.Itoa(os.Getpid()))
				i += 2
				continue
			}
			if isAlphaOrUnderscore(s[i+1]) {
				j := i + 1
				for j < len(s) && isAlnumOrUnderscore(s[j]) {
					j++
				}
				varName := s[i+1 : j]
				val := os.Getenv(varName)
				if val == "" {
					result.WriteString(s[i:j])
				} else {
					result.WriteString(val)
				}
				i = j
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

func isAlphaOrUnderscore(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isAlnumOrUnderscore(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

func executeChain(chain []chainEntry, cfg *Config) {
	for _, entry := range chain {
		if entry.operator == "&&" && lastExitCode != 0 {
			continue
		}
		if entry.operator == "||" && lastExitCode == 0 {
			continue
		}

		tokens := expandVariables(entry.args)
		if len(tokens) == 0 {
			continue
		}

		background := false
		if tokens[len(tokens)-1] == "&" {
			background = true
			tokens = tokens[:len(tokens)-1]
		}

		segments := splitPipes(tokens)
		if len(segments) == 1 {
			if isBuiltin(segments[0][0]) {
				executeBuiltin(segments[0], cfg)
			} else {
				executeSimple(segments[0], background)
			}
		} else {
			executePipeline(segments, background)
		}
	}
}

func splitPipes(tokens []string) [][]string {
	var segments [][]string
	var current []string
	for _, t := range tokens {
		if t == "|" {
			segments = append(segments, current)
			current = nil
		} else {
			current = append(current, t)
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}
	return segments
}

func tokenize(line string) []string {
	inSingle := false
	inDouble := false
	var current strings.Builder
	var tokens []string

	for _, ch := range line {
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' || ch == '\t':
			if !inSingle && !inDouble {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				continue
			}
			current.WriteRune(ch)
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func isBuiltin(cmd string) bool {
	switch cmd {
	case "exit", "cd", "pwd", "jobs", "export", "lash":
		return true
	}
	return false
}

func executeBuiltin(args []string, cfg *Config) {
	switch args[0] {
	case "exit":
		code := lastExitCode
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err == nil {
				code = n
			}
		}
		os.Exit(code)
	case "cd":
		dir := ""
		if len(args) > 1 {
			dir = args[1]
		} else {
			dir = os.Getenv("HOME")
		}
		if err := os.Chdir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "cd: %s\n", err)
			lastExitCode = 1
		} else {
			lastExitCode = 0
		}
	case "pwd":
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "pwd: %s\n", err)
			lastExitCode = 1
		} else {
			fmt.Println(dir)
			lastExitCode = 0
		}
	case "jobs":
		if len(backgroundPids) == 0 {
			lastExitCode = 0
			return
		}
		for pid := range backgroundPids {
			fmt.Printf("[%d] running\n", pid)
		}
		lastExitCode = 0
	case "export":
		if len(args) < 2 {
			lastExitCode = 1
			return
		}
		for _, a := range args[1:] {
			eqIdx := strings.Index(a, "=")
			if eqIdx < 1 {
				lastExitCode = 1
				continue
			}
			key := a[:eqIdx]
			val := a[eqIdx+1:]
			os.Setenv(key, val)
			lastExitCode = 0
		}
	case "lash":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "lash: usage: lash <set-config|version> [args...]")
			lastExitCode = 1
			return
		}
		switch args[1] {
		case "set-config":
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "lash: usage: lash set-config <key> <value>")
				lastExitCode = 1
				return
			}
			key := args[2]
			val := args[3]
			if !cfg.Set(key, val) {
				fmt.Fprintf(os.Stderr, "lash: unknown config key: %s\n", key)
				lastExitCode = 1
				return
			}
			if err := cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "lash: failed to save config: %s\n", err)
				lastExitCode = 1
				return
			}
			fmt.Printf("lash: set %s = %s\n", key, val)
			lastExitCode = 0
		case "version":
			printVersion()
			lastExitCode = 0
		default:
			fmt.Fprintf(os.Stderr, "lash: unknown subcommand: %s\n", args[1])
			lastExitCode = 1
		}
	}
}

func parseRedirection(args []string) (cmdArgs []string, stdin string, stdout string, appendMode bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "<" && i+1 < len(args) {
			stdin = args[i+1]
			i++
		} else if args[i] == ">>" && i+1 < len(args) {
			stdout = args[i+1]
			appendMode = true
			i++
		} else if args[i] == ">" && i+1 < len(args) {
			stdout = args[i+1]
			i++
		} else {
			cmdArgs = append(cmdArgs, args[i])
		}
	}
	return
}

func executeSimple(args []string, background bool) {
	cmdArgs, inFile, outFile, appendMode := parseRedirection(args)
	if len(cmdArgs) == 0 {
		return
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if inFile != "" {
		f, err := os.Open(inFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
			return
		}
		defer f.Close()
		cmd.Stdin = f
	}

	if outFile != "" {
		flag := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flag |= os.O_APPEND
		} else {
			flag |= os.O_TRUNC
		}
		f, err := os.OpenFile(outFile, flag, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
			return
		}
		defer f.Close()
		cmd.Stdout = f
	}

	if background {
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
			return
		}
		backgroundPids[cmd.Process.Pid] = cmd
		fmt.Printf("[%d]\n", cmd.Process.Pid)
		lastExitCode = 0
		return
	}

	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			fmt.Fprintf(os.Stderr, "lash: %s: command not found\n", cmdArgs[0])
		}
	}
	lastExitCode = getExitCode(err)
}

func executePipeline(segments [][]string, background bool) {
	type pipePair struct {
		r *os.File
		w *os.File
	}

	pipes := make([]pipePair, len(segments)-1)
	for i := range pipes {
		r, w, err := os.Pipe()
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
			return
		}
		pipes[i] = pipePair{r, w}
	}

	var cmds []*exec.Cmd
	for i, seg := range segments {
		cmdArgs, inFile, outFile, appendMode := parseRedirection(seg)
		if len(cmdArgs) == 0 {
			return
		}

		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.Stderr = os.Stderr

		if inFile != "" {
			f, err := os.Open(inFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
				return
			}
			defer f.Close()
			cmd.Stdin = f
		} else if i == 0 {
			cmd.Stdin = os.Stdin
		} else {
			cmd.Stdin = pipes[i-1].r
		}

		if outFile != "" {
			flag := os.O_CREATE | os.O_WRONLY
			if appendMode {
				flag |= os.O_APPEND
			} else {
				flag |= os.O_TRUNC
			}
			f, err := os.OpenFile(outFile, flag, 0644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
				return
			}
			defer f.Close()
			cmd.Stdout = f
		} else if i == len(segments)-1 {
			cmd.Stdout = os.Stdout
		} else {
			cmd.Stdout = pipes[i].w
		}

		cmds = append(cmds, cmd)
	}

	for _, cmd := range cmds {
		cmd.Start()
	}

	for _, p := range pipes {
		p.w.Close()
	}

	lastExit := 0
	for _, cmd := range cmds {
		err := cmd.Wait()
		lastExit = getExitCode(err)
	}
	lastExitCode = lastExit

	for _, p := range pipes {
		p.r.Close()
	}
}
