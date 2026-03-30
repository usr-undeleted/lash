package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var lastExitCode int = 0
var cmdNumber int = 0
var pendingNotifs []string
var notifMu sync.Mutex

const defaultPS1 = `\[\e[1;36m\]\u@\h\[\e[0m\] \[\e[1;33m\]\w\[\e[0m\] on \[\e[1m\]\g\[\e[0m\]\x\n\[\e[1m\]╰\$\[\e[0m\] `

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
	ps1 := os.Getenv("PS1")
	if ps1 == "" {
		ps1 = defaultPS1
	}
	return expandPS1(ps1)
}

func expandPS1(ps1 string) string {
	fillIdx := strings.Index(ps1, `\f`)
	if fillIdx >= 0 {
		left := ps1[:fillIdx]
		right := ps1[fillIdx+2:]
		leftExpanded := expandPS1Escapes(left)
		rightExpanded := expandPS1Escapes(right)
		termW := getTermWidth()
		if termW <= 0 {
			termW = 80
		}
		leftLineW := lastLineWidth(leftExpanded)
		rightLineW := firstLineWidth(rightExpanded)
		if leftLineW+rightLineW >= termW {
			return leftExpanded + " " + rightExpanded
		}
		gap := termW - leftLineW - rightLineW
		return leftExpanded + strings.Repeat(" ", gap) + rightExpanded
	}
	return expandPS1Escapes(ps1)
}

func expandPS1Escapes(ps1 string) string {
	var b strings.Builder
	runes := []rune(ps1)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '\\' {
			b.WriteRune(runes[i])
			continue
		}
		i++
		if i >= len(runes) {
			b.WriteByte('\\')
			break
		}
		switch runes[i] {
		case 'u':
			b.WriteString(os.Getenv("USER"))
		case 'h':
			h, _ := os.Hostname()
			if idx := strings.Index(h, "."); idx >= 0 {
				h = h[:idx]
			}
			b.WriteString(h)
		case 'H':
			h, _ := os.Hostname()
			b.WriteString(h)
		case 'w':
			dir, err := os.Getwd()
			if err != nil {
				dir = "?"
			}
			home := os.Getenv("HOME")
			if home != "" && strings.HasPrefix(dir, home) {
				dir = "~" + dir[len(home):]
			}
			b.WriteString(dir)
		case 'W':
			dir, err := os.Getwd()
			if err != nil {
				dir = "?"
			}
			b.WriteString(filepath.Base(dir))
		case 'n':
			b.WriteString("\r\n")
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteString(time.Now().Format("15:04:05"))
		case 'T':
			b.WriteString(time.Now().Format("15:04:05"))
		case '@':
			b.WriteString(time.Now().Format("15:04"))
		case 'd':
			b.WriteString(time.Now().Format("Mon Jan 02"))
		case 'D':
			if i+2 < len(runes) {
				fmtStr := strings.ReplaceAll(string(runes[i+1:i+3]), "%", "%%")
				b.WriteString(time.Now().Format(fmtStr))
				i += 2
			} else {
				b.WriteString(time.Now().Format("Mon Jan 02"))
			}
		case '$':
			if os.Getuid() == 0 {
				b.WriteByte('#')
			} else {
				b.WriteByte('$')
			}
		case '\\':
			b.WriteByte('\\')
		case 'e':
			b.WriteByte('\x1b')
		case 'a':
			b.WriteByte('\x07')
		case '[':
		case ']':
		case 'g':
			branch := getGitBranch()
			if branch != "" {
				b.WriteString(branch)
			}
		case 'x':
			if lastExitCode >= 1 {
				b.WriteString(fmt.Sprintf("%s✗%s", colorRed, colorReset))
			}
		case '!':
			b.WriteString(strconv.Itoa(cmdNumber))
		case '#':
			b.WriteString(strconv.Itoa(cmdNumber))
		case '0', '1', '2', '3', '4', '5', '6', '7':
			if i+2 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '7' && runes[i+2] >= '0' && runes[i+2] <= '7' {
				val := int(runes[i]-'0')*64 + int(runes[i+1]-'0')*8 + int(runes[i+2]-'0')
				if val > 0 && val < 256 {
					b.WriteByte(byte(val))
					i += 2
				} else {
					b.WriteByte('\\')
					b.WriteRune(runes[i])
				}
			} else {
				b.WriteByte('\\')
				b.WriteRune(runes[i])
			}
		default:
			b.WriteByte('\\')
			b.WriteRune(runes[i])
		}
	}
	return b.String()
}

func reapZombies() {
	jobMu.Lock()
	var bgPids []int
	for _, j := range jobTable {
		if j.State == JobRunning && !isForegroundPID(j.PID) {
			bgPids = append(bgPids, j.PID)
		}
	}
	jobMu.Unlock()

	for _, pid := range bgPids {
		var status syscall.WaitStatus
		wp, err := syscall.Wait4(pid, &status, syscall.WNOHANG|syscall.WUNTRACED, nil)
		if wp <= 0 || err != nil {
			continue
		}
		handleChildReap(pid, status)
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

func sourceLashrc(cfg *Config) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".lashrc")
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = expandAliasLine(line)
		if line == "" {
			continue
		}
		tokens := tokenize(line)
		if len(tokens) > 0 && (tokens[0] == "alias" || tokens[0] == "unalias") {
			executeBuiltin(tokens, cfg)
			continue
		}
		chains := splitChains(line)
		for _, chain := range chains {
			executeChain(chain, cfg)
		}
	}
}

func main() {
	initJobControl()
	initAliases()
	os.Setenv("PS1", defaultPS1)

	cfg := LoadConfig()
	sourceLashrc(cfg)
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
		line = expandAliasLine(line)
		if line == "" {
			continue
		}

		tokens := tokenize(line)
		if len(tokens) > 0 && (tokens[0] == "alias" || tokens[0] == "unalias") {
			executeBuiltin(tokens, cfg)
			continue
		}

		chains := splitChains(line)
		cmdNumber++
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

func expandGlobs(tokens []string) []string {
	var result []string
	for _, t := range tokens {
		if strings.ContainsAny(t, "*?[") {
			matches, err := filepath.Glob(t)
			if err == nil && len(matches) > 0 {
				sort.Strings(matches)
				result = append(result, matches...)
			} else {
				result = append(result, t)
			}
		} else {
			result = append(result, t)
		}
	}
	return result
}

func expandString(s string) string {
	if len(s) > 0 && s[0] == '~' {
		if len(s) == 1 || s[1] == '/' {
			home := os.Getenv("HOME")
			if home != "" {
				s = home + s[1:]
			}
		}
	}
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
					result.WriteString(os.Getenv(varName))
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
				result.WriteString(os.Getenv(varName))
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
		tokens = expandGlobs(tokens)
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
	bytes := []byte(line)

	flushCurrent := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for i := 0; i < len(bytes); i++ {
		ch := rune(bytes[i])

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteRune(ch)
			continue
		}

		if inSingle || inDouble {
			current.WriteRune(ch)
			continue
		}

		if ch == ' ' || ch == '\t' {
			flushCurrent()
			continue
		}

		switch ch {
		case ';', '|', '<':
			flushCurrent()
			tokens = append(tokens, string(ch))
		case '&':
			if i+1 < len(bytes) && bytes[i+1] == '&' {
				flushCurrent()
				tokens = append(tokens, "&&")
				i++
			} else {
				flushCurrent()
				tokens = append(tokens, "&")
			}
		case '>':
			if i+1 < len(bytes) && bytes[i+1] == '>' {
				flushCurrent()
				tokens = append(tokens, ">>")
				i++
			} else {
				flushCurrent()
				tokens = append(tokens, ">")
			}
		default:
			current.WriteRune(ch)
		}
	}
	flushCurrent()
	for i, t := range tokens {
		if len(t) >= 2 {
			if (t[0] == '\'' && t[len(t)-1] == '\'') ||
				(t[0] == '"' && t[len(t)-1] == '"') {
				tokens[i] = t[1 : len(t)-1]
			}
		}
	}
	return tokens
}

func isBuiltin(cmd string) bool {
	switch cmd {
	case "exit", "cd", "pwd", "jobs", "fg", "bg", "kill", "export", "lash",
		"echo", "true", "false", "unset", "env", "type", "which", "alias", "unalias":
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
		printJobs()
		lastExitCode = 0
	case "fg":
		jobSpec := ""
		if len(args) > 1 {
			jobSpec = args[1]
		}
		var job *Job
		if jobSpec != "" {
			var err error
			job, err = parseJobSpec(jobSpec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "fg: %s\n", err)
				lastExitCode = 1
				return
			}
		} else {
			job = getMostRecentJob()
		}
		if job == nil {
			fmt.Fprintln(os.Stderr, "fg: current: no such job")
			lastExitCode = 1
			return
		}
		setFgPIDs([]int{job.PID})
		markJobRunningByPID(job.PID)
		exitCode := waitForeground([]int{job.PID}, job.PGID, job.Command)
		clearFgPIDs()
		if exitCode < 0 {
			lastExitCode = 128 + int(syscall.SIGTSTP)
		} else {
			lastExitCode = exitCode
		}
	case "bg":
		jobSpec := ""
		if len(args) > 1 {
			jobSpec = args[1]
		}
		var job *Job
		if jobSpec != "" {
			var err error
			job, err = parseJobSpec(jobSpec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bg: %s\n", err)
				lastExitCode = 1
				return
			}
		} else {
			job = getMostRecentJob()
		}
		if job == nil {
			fmt.Fprintln(os.Stderr, "bg: current: no such job")
			lastExitCode = 1
			return
		}
		if job.State == JobStopped {
			markJobRunningByPID(job.PID)
			syscall.Kill(-job.PGID, syscall.SIGCONT)
		}
		fmt.Printf("[%d]+ %s &\n", job.Number, job.Command)
		lastExitCode = 0
	case "kill":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "kill: usage: kill [-s signal] pid ...")
			lastExitCode = 1
			return
		}
		if args[1] == "-l" {
			for _, sig := range listSignals() {
				fmt.Printf("%2d) %s\n", sig, strings.TrimPrefix(sig.String(), "SIG"))
			}
			lastExitCode = 0
			return
		}
		sig := syscall.SIGTERM
		i := 1
		if strings.HasPrefix(args[1], "-") && len(args[1]) > 1 {
			sigName := args[1][1:]
			if len(sigName) > 1 && sigName[0] == 's' {
				sigName = sigName[1:]
				if len(sigName) == 0 {
					fmt.Fprintln(os.Stderr, "kill: -s requires an argument")
					lastExitCode = 1
					return
				}
			}
			parsed, err := parseSignal(sigName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kill: %s\n", err)
				lastExitCode = 1
				return
			}
			sig = parsed
			i = 2
		}
		if i >= len(args) {
			fmt.Fprintln(os.Stderr, "kill: usage: kill [-s signal] pid ...")
			lastExitCode = 1
			return
		}
		lastExitCode = 0
		for _, a := range args[i:] {
			if len(a) >= 2 && a[0] == '%' {
				job, err := parseJobSpec(a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "kill: %s\n", err)
					lastExitCode = 1
					continue
				}
				if err := syscall.Kill(-job.PGID, sig); err != nil {
					fmt.Fprintf(os.Stderr, "kill: %d: %s\n", job.PGID, err)
					lastExitCode = 1
				}
			} else {
				pid, err := strconv.Atoi(a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "kill: %s: invalid pid\n", a)
					lastExitCode = 1
					continue
				}
				if err := syscall.Kill(pid, sig); err != nil {
					fmt.Fprintf(os.Stderr, "kill: %d: %s\n", pid, err)
					lastExitCode = 1
				}
			}
		}
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
			if len(val) >= 2 && ((val[0] == '\'' && val[len(val)-1] == '\'') || (val[0] == '"' && val[len(val)-1] == '"')) {
				val = val[1 : len(val)-1]
			}
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
			if len(args) == 3 && args[2] == "list" {
				fmt.Println("syntax-color = <0|1>   highlight commands green/red as you type")
				fmt.Println("logosize    = <mini|small|big>   logo size for lash version")
				lastExitCode = 0
				return
			}
			if len(args) < 4 {
				fmt.Fprintln(os.Stderr, "lash: usage: lash set-config <key> <value>")
				fmt.Fprintln(os.Stderr, "       lash set-config list")
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
			printVersion(cfg.LogoSize)
			lastExitCode = 0
		default:
			fmt.Fprintf(os.Stderr, "lash: unknown subcommand: %s\n", args[1])
			lastExitCode = 1
		}
	case "echo":
		noNewline := false
		interpretEscapes := false
		i := 1
		for i < len(args) {
			if args[i] == "-n" {
				noNewline = true
				i++
			} else if args[i] == "-e" {
				interpretEscapes = true
				i++
			} else if args[i] == "-ne" || args[i] == "-en" {
				noNewline = true
				interpretEscapes = true
				i++
			} else {
				break
			}
		}
		output := strings.Join(args[i:], " ")
		if interpretEscapes {
			output = interpretEscapeSequences(output)
		}
		if noNewline {
			fmt.Print(output)
		} else {
			fmt.Println(output)
		}
		lastExitCode = 0
	case "true":
		lastExitCode = 0
	case "false":
		lastExitCode = 1
	case "unset":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "unset: usage: unset <var> [var...]")
			lastExitCode = 1
			return
		}
		for _, a := range args[1:] {
			os.Unsetenv(a)
		}
		lastExitCode = 0
	case "env":
		for _, e := range os.Environ() {
			fmt.Println(e)
		}
		lastExitCode = 0
	case "type":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "type: usage: type <command>")
			lastExitCode = 1
			return
		}
		for _, a := range args[1:] {
			if isBuiltin(a) {
				fmt.Printf("%s is a shell builtin\n", a)
			} else {
				path, err := exec.LookPath(a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "type: %s: not found\n", a)
					lastExitCode = 1
					return
				}
				fmt.Printf("%s is %s\n", a, path)
			}
		}
		lastExitCode = 0
	case "which":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "which: usage: which <command>")
			lastExitCode = 1
			return
		}
		for _, a := range args[1:] {
			path, err := exec.LookPath(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "which: no %s in (%s)\n", a, os.Getenv("PATH"))
				lastExitCode = 1
				return
			}
			fmt.Println(path)
		}
		lastExitCode = 0
	case "alias":
		if len(args) == 1 {
			printAliases()
			lastExitCode = 0
			return
		}
		rest := strings.Join(args[1:], " ")
		eqIdx := strings.Index(rest, "=")
		if eqIdx < 1 {
			fmt.Fprintf(os.Stderr, "alias: usage: alias name=\"value\"\n")
			lastExitCode = 1
			return
		}
		name := strings.TrimSpace(rest[:eqIdx])
		value := rest[eqIdx+1:]
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '\'' && value[len(value)-1] == '\'') ||
				(value[0] == '"' && value[len(value)-1] == '"') {
				value = value[1 : len(value)-1]
			}
		}
		alias, err := parseAliasDefinition(name, value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "alias: %s\n", err)
			lastExitCode = 1
			return
		}
		aliasMu.Lock()
		aliasTable[name] = alias
		aliasMu.Unlock()
		lastExitCode = 0
	case "unalias":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "unalias: usage: unalias name [name...]")
			lastExitCode = 1
			return
		}
		for _, a := range args[1:] {
			if !removeAlias(a) {
				fmt.Fprintf(os.Stderr, "unalias: %s: not found\n", a)
				lastExitCode = 1
				continue
			}
		}
	}
}

func interpretEscapeSequences(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				result.WriteByte('\n')
				i += 2
			case 't':
				result.WriteByte('\t')
				i += 2
			case 'r':
				result.WriteByte('\r')
				i += 2
			case '\\':
				result.WriteByte('\\')
				i += 2
			case 'a':
				result.WriteByte('\a')
				i += 2
			case 'b':
				result.WriteByte('\b')
				i += 2
			case 'f':
				result.WriteByte('\f')
				i += 2
			case 'v':
				result.WriteByte('\v')
				i += 2
			case '0':
				result.WriteByte(0)
				i += 2
			default:
				result.WriteByte(s[i])
				i++
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

	err := cmd.Start()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			fmt.Fprintf(os.Stderr, "lash: %s: command not found\n", cmdArgs[0])
			lastExitCode = 127
		} else {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
		}
		return
	}

	commandStr := strings.Join(cmdArgs, " ")

	if background {
		pid := cmd.Process.Pid
		job := addJob(pid, pid, JobRunning, commandStr)
		fmt.Printf("[%d] %d\n", job.Number, pid)
		lastExitCode = 0
		return
	}

	exitCode := waitForeground([]int{cmd.Process.Pid}, cmd.Process.Pid, commandStr)
	if exitCode < 0 {
		lastExitCode = 128 + int(syscall.SIGTSTP)
	} else {
		lastExitCode = exitCode
	}
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
	var cmdArgsList [][]string
	for i, seg := range segments {
		cmdArgs, inFile, outFile, appendMode := parseRedirection(seg)
		if len(cmdArgs) == 0 {
			return
		}

		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
		cmdArgsList = append(cmdArgsList, cmdArgs)
	}

	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
			return
		}
	}

	for _, p := range pipes {
		p.w.Close()
	}

	pids := make([]int, len(cmds))
	for i, cmd := range cmds {
		pids[i] = cmd.Process.Pid
	}

	pgid := pids[0]

	commandStr := strings.Join(segments[0], " ")
	for _, seg := range segments[1:] {
		commandStr += " | " + strings.Join(seg, " ")
	}

	if background {
		job := addJob(pids[0], pgid, JobRunning, commandStr)
		fmt.Printf("[%d] %d\n", job.Number, pids[0])

		go func() {
			for _, pid := range pids {
				var status syscall.WaitStatus
				for {
					wp, we := syscall.Wait4(pid, &status, 0, nil)
					if we == syscall.EINTR {
						continue
					}
					if wp == pid {
						break
					}
					break
				}
			}
		}()

		for _, p := range pipes {
			p.r.Close()
		}
		lastExitCode = 0
		return
	}

	exitCode := waitForeground(pids, pgid, commandStr)
	if exitCode < 0 {
		lastExitCode = 128 + int(syscall.SIGTSTP)
	} else {
		lastExitCode = exitCode
	}

	for _, p := range pipes {
		p.r.Close()
	}
}
