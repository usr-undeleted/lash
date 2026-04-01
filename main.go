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
var expandError bool
var cmdNumber int = 0
var returnFlag bool
var pendingNotifs []string
var notifMu sync.Mutex

var varTable map[string]string
var exportedVars map[string]bool
var varMu sync.Mutex

const defaultPS1 = `╭─| \l | \033[36m\u\033[0m@\033[36m\h\033[0m | \033[36m\w\033[0m \g{| \033[33m\g\033[31m\G!\033[0m }|\F─\X{❯ \X \033[31m\x↵\033[0m }❮\n╰─$ `

func initVarTable() {
	varTable = make(map[string]string)
	exportedVars = make(map[string]bool)
	for _, env := range os.Environ() {
		if idx := strings.Index(env, "="); idx >= 0 {
			key := env[:idx]
			val := env[idx+1:]
			varTable[key] = val
			exportedVars[key] = true
		}
	}
}

func getVar(name string) string {
	varMu.Lock()
	defer varMu.Unlock()
	if val, ok := varTable[name]; ok {
		return val
	}
	return os.Getenv(name)
}

func setVar(name, value string, exported bool) {
	varMu.Lock()
	defer varMu.Unlock()
	varTable[name] = value
	if exported {
		exportedVars[name] = true
		os.Setenv(name, value)
	}
	if !exported {
		if exportedVars[name] {
			os.Setenv(name, value)
		}
	}
}

func unsetVar(name string) {
	varMu.Lock()
	defer varMu.Unlock()
	delete(varTable, name)
	delete(exportedVars, name)
	os.Unsetenv(name)
}

func isExported(name string) bool {
	varMu.Lock()
	defer varMu.Unlock()
	return exportedVars[name]
}

func isValidVarName(name string) bool {
	if len(name) == 0 {
		return false
	}
	if !isAlphaOrUnderscore(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isAlnumOrUnderscore(name[i]) {
			return false
		}
	}
	return true
}

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

func isGitDirty() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	for {
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			cmd := exec.Command("git", "status", "--porcelain")
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				return false
			}
			return len(out) > 0
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

func getPrompt() string {
	ps1 := os.Getenv("PS1")
	if ps1 == "" {
		ps1 = defaultPS1
	}
	return expandPS1(ps1)
}

func expandPS1(ps1 string) string {
	var result string

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
			result = leftExpanded + " " + rightExpanded
		} else {
			gap := termW - leftLineW - rightLineW
			result = leftExpanded + strings.Repeat(" ", gap) + rightExpanded
		}
	} else {
		fillFIdx := strings.Index(ps1, `\F`)
		if fillFIdx >= 0 {
			runes := []rune(ps1[fillFIdx+2:])
			if len(runes) > 0 {
				fillChar := runes[0]
				afterFill := string(runes[1:])
				left := ps1[:fillFIdx]
				right := afterFill
				leftExpanded := expandPS1Escapes(left)
				rightExpanded := expandPS1Escapes(right)
				termW := getTermWidth()
				if termW <= 0 {
					termW = 80
				}
				leftLineW := lastLineWidth(leftExpanded)
				rightLineW := firstLineWidth(rightExpanded)
				if leftLineW+rightLineW >= termW {
					result = leftExpanded + " " + rightExpanded
				} else {
					gap := termW - leftLineW - rightLineW
					result = leftExpanded + strings.Repeat(string(fillChar), gap) + rightExpanded
				}
			} else {
				result = expandPS1Escapes(ps1)
			}
		} else {
			result = expandPS1Escapes(ps1)
		}
	}

	termW := getTermWidth()
	if termW <= 0 {
		termW = 80
	}
	if lastLineWidth(result) == termW {
		result += "\r\n"
	}
	return result
}

func expandTimeFormat(format string) string {
	now := time.Now()
	var b strings.Builder
	runes := []rune(format)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '%' && i+1 < len(runes) {
			code := string(runes[i+1:])
			switch {
			case strings.HasPrefix(code, "yyyy"):
				b.WriteString(fmt.Sprintf("%04d", now.Year()))
				i += 4
			case strings.HasPrefix(code, "yy"):
				b.WriteString(fmt.Sprintf("%02d", now.Year()%100))
				i += 2
			case strings.HasPrefix(code, "hh"):
				b.WriteString(fmt.Sprintf("%02d", now.Hour()))
				i += 2
			case strings.HasPrefix(code, "HH"):
				h := now.Hour() % 12
				if h == 0 {
					h = 12
				}
				b.WriteString(fmt.Sprintf("%02d", h))
				i += 2
			case strings.HasPrefix(code, "MM"):
				b.WriteString(fmt.Sprintf("%02d", now.Minute()))
				i += 2
			case strings.HasPrefix(code, "dd"):
				b.WriteString(fmt.Sprintf("%02d", now.Day()))
				i += 2
			case strings.HasPrefix(code, "mm"):
				b.WriteString(fmt.Sprintf("%02d", now.Month()))
				i += 2
			default:
				b.WriteRune(runes[i])
			}
		} else {
			b.WriteRune(runes[i])
		}
	}
	return b.String()
}

func tryParseBrace(runes []rune, pos int) (content string, consumed int, ok bool) {
	if pos >= len(runes) || runes[pos] != '{' {
		return "", 0, false
	}
	depth := 1
	for j := pos + 1; j < len(runes); j++ {
		ch := runes[j]
		if ch == '\\' && j+1 < len(runes) {
			j++
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return string(runes[pos+1 : j]), j - pos + 1, true
			}
		}
	}
	return "", 0, false
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
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				if content != "" {
					b.WriteString(expandTimeFormat(content))
				}
				i += consumed
			} else {
				now := time.Now()
				b.WriteString(fmt.Sprintf("%02d%02d%02d", now.Day(), now.Month(), now.Year()%100))
			}
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
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				if getGitBranch() != "" {
					b.WriteString(expandPS1Escapes(content))
				}
				i += consumed
			} else {
				branch := getGitBranch()
				if branch != "" {
					b.WriteString(branch)
				}
			}
		case 'G':
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				if isGitDirty() {
					b.WriteString(expandPS1Escapes(content))
				}
				i += consumed
			} else if i+1 < len(runes) {
				if isGitDirty() {
					b.WriteRune(runes[i+1])
				}
				i++
			}
		case 'F':
			if i+1 < len(runes) {
				i++
			}
		case 'x':
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				if lastExitCode >= 1 {
					b.WriteString(expandPS1Escapes(content))
				}
				i += consumed
			} else if i+1 < len(runes) {
				if lastExitCode >= 1 {
					b.WriteRune(runes[i+1])
				}
				i++
			}
		case 'X':
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				if lastExitCode >= 1 {
					b.WriteString(expandPS1Escapes(content))
				}
				i += consumed
			} else if lastExitCode >= 1 {
				b.WriteString(fmt.Sprintf("%s%d%s", colorRed, lastExitCode, colorReset))
			}
		case 'l':
			b.WriteString(getOSIcon())
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

func sourceFile(path string, cfg *Config) int {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "lash: source: %s: No such file or directory\n", path)
		} else {
			fmt.Fprintf(os.Stderr, "lash: source: %s: %s\n", path, err)
		}
		return 1
	}
	defer f.Close()

	returnFlag = false
	scanner := bufio.NewScanner(f)
	code := 0
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
			code = lastExitCode
			if returnFlag {
				break
			}
			continue
		}
		chains := splitChains(line)
		for _, chain := range chains {
			executeChain(chain, cfg)
			if returnFlag {
				break
			}
		}
		code = lastExitCode
		if returnFlag {
			break
		}
	}
	return code
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
	f.Close()
	sourceFile(path, cfg)
}

func handleGlobalCommand(args []string) {
	sub := args[0]
	switch sub {
	case "version":
		cfg := LoadConfig()
		printVersion(cfg.LogoSize)
		os.Exit(0)
	case "set-config":
		if len(args) >= 2 && args[1] == "list" {
			fmt.Println("syntax-color = <0|1>   highlight commands green/red as you type")
			fmt.Println("logosize    = <mini|small|big>   logo size for lash version")
			os.Exit(0)
		}
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "lash: usage: lash set-config <key> <value>")
			fmt.Fprintln(os.Stderr, "       lash set-config list")
			os.Exit(1)
		}
		cfg := LoadConfig()
		key := args[1]
		val := args[2]
		if !cfg.Set(key, val) {
			fmt.Fprintf(os.Stderr, "lash: unknown config key: %s\n", key)
			os.Exit(1)
		}
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "lash: failed to save config: %s\n", err)
			os.Exit(1)
		}
		fmt.Printf("lash: set %s = %s\n", key, val)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "lash: unknown subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: lash [version|set-config ...]")
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) > 1 {
		handleGlobalCommand(os.Args[1:])
	}

	initJobControl()
	initAliases()
	initVarTable()
	os.Setenv("PS1", defaultPS1)
	setVar("PS1", defaultPS1, true)

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

func executeChain(chain []chainEntry, cfg *Config) {
	for _, entry := range chain {
		if entry.operator == "&&" && lastExitCode != 0 {
			continue
		}
		if entry.operator == "||" && lastExitCode == 0 {
			continue
		}

		tokens := expandBraces(entry.args)
		tokens = expandVariables(tokens)
		if expandError {
			expandError = false
			lastExitCode = 2
			waitProcSubst()
			continue
		}
		tokens = expandGlobs(tokens)
		if len(tokens) == 0 {
			continue
		}

		background := false
		if tokens[len(tokens)-1] == "&" {
			background = true
			tokens = tokens[:len(tokens)-1]
		}

		assignEnd := 0
		for _, t := range tokens {
			eqIdx := strings.Index(t, "=")
			if eqIdx < 1 {
				break
			}
			name := t[:eqIdx]
			if !isValidVarName(name) {
				break
			}
			assignEnd++
		}

		if assignEnd > 0 {
			assignments := tokens[:assignEnd]
			rest := tokens[assignEnd:]

			if len(rest) == 0 {
				for _, a := range assignments {
					eqIdx := strings.Index(a, "=")
					name := a[:eqIdx]
					val := a[eqIdx+1:]
					setVar(name, val, false)
				}
				lastExitCode = 0
				continue
			}

			prefixEnv := make(map[string]string)
			for _, a := range assignments {
				eqIdx := strings.Index(a, "=")
				name := a[:eqIdx]
				val := a[eqIdx+1:]
				prefixEnv[name] = val
			}

			segments := splitPipes(rest)
			if len(segments) == 1 {
				if isBuiltin(segments[0][0]) {
					for name, val := range prefixEnv {
						setVar(name, val, false)
					}
					executeBuiltin(segments[0], cfg)
					for name := range prefixEnv {
						unsetVar(name)
					}
				} else {
					executeSimpleWithEnv(segments[0], background, prefixEnv)
				}
			} else {
				executePipelineWithEnv(segments, background, prefixEnv)
			}
			waitProcSubst()
			continue
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
		waitProcSubst()
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
	substDepth := 0
	substInSingle := false
	substInDouble := false
	procSubstDepth := 0
	procSubstInSingle := false
	procSubstInDouble := false

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

		if procSubstDepth > 0 {
			if procSubstInSingle {
				if bytes[i] == '\'' {
					procSubstInSingle = false
				}
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\'' && !procSubstInDouble {
				procSubstInSingle = true
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '"' && !procSubstInSingle {
				procSubstInDouble = !procSubstInDouble
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\\' && procSubstInDouble && i+1 < len(bytes) {
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '(' && !procSubstInSingle && !procSubstInDouble {
				procSubstDepth++
			}
			if bytes[i] == ')' && !procSubstInSingle && !procSubstInDouble {
				procSubstDepth--
				if procSubstDepth == 0 {
					current.WriteByte(bytes[i])
					continue
				}
			}
			current.WriteByte(bytes[i])
			continue
		}

		if substDepth > 0 {
			if substInSingle {
				if bytes[i] == '\'' {
					substInSingle = false
				}
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\'' && !substInDouble {
				substInSingle = true
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '"' && !substInSingle {
				substInDouble = !substInDouble
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\\' && substInDouble && i+1 < len(bytes) {
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '(' && !substInSingle && !substInDouble {
				substDepth++
			}
			if bytes[i] == ')' && !substInSingle && !substInDouble {
				substDepth--
				if substDepth == 0 {
					current.WriteByte(bytes[i])
					continue
				}
			}
			current.WriteByte(bytes[i])
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(bytes[i])
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(bytes[i])
			continue
		}

		if inSingle || inDouble {
			current.WriteByte(bytes[i])
			continue
		}

		if ch == '\\' && i+1 < len(bytes) {
			current.WriteByte(bytes[i])
			i++
			current.WriteByte(bytes[i])
			continue
		}

		if ch == ' ' || ch == '\t' {
			flushCurrent()
			continue
		}

		switch ch {
		case '<':
			if i+1 < len(bytes) && bytes[i+1] == '(' {
				flushCurrent()
				procSubstDepth = 1

				procSubstInSingle = false
				procSubstInDouble = false
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			flushCurrent()
			tokens = append(tokens, string(ch))
		case '>':
			if i+1 < len(bytes) && bytes[i+1] == '>' {
				flushCurrent()
				tokens = append(tokens, ">>")
				i++
			} else if i+1 < len(bytes) && bytes[i+1] == '(' {
				flushCurrent()
				procSubstDepth = 1

				procSubstInSingle = false
				procSubstInDouble = false
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			} else {
				flushCurrent()
				tokens = append(tokens, ">")
			}
		case ';', '|':
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
		case '$':
			if i+1 < len(bytes) && bytes[i+1] == '(' {
				if i+2 < len(bytes) && bytes[i+2] == '(' {
					substDepth = 1
					current.WriteByte(bytes[i])
					i++
					current.WriteByte(bytes[i])
					i++
					current.WriteByte(bytes[i])
					continue
				}
				substDepth = 1
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			current.WriteByte(bytes[i])
		case '`':
			inBacktick := true
			current.WriteByte(bytes[i])
			for i++; i < len(bytes) && inBacktick; i++ {
				if bytes[i] == '\\' && i+1 < len(bytes) {
					current.WriteByte(bytes[i])
					i++
					current.WriteByte(bytes[i])
					continue
				}
				if bytes[i] == '`' {
					inBacktick = false
				}
				current.WriteByte(bytes[i])
			}
			i--
		default:
			current.WriteByte(bytes[i])
		}
	}
	flushCurrent()
	return tokens
}

func isBuiltin(cmd string) bool {
	switch cmd {
	case "exit", "cd", "pwd", "jobs", "fg", "bg", "kill", "export", "lash",
		"echo", "true", "false", "unset", "env", "type", "which", "alias", "unalias",
		"source", ".", "return":
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
			if eqIdx < 0 {
				if isValidVarName(a) {
					varMu.Lock()
					exportedVars[a] = true
					varMu.Unlock()
					if val, ok := varTable[a]; ok {
						os.Setenv(a, val)
					}
					lastExitCode = 0
				} else {
					lastExitCode = 1
				}
				continue
			}
			key := a[:eqIdx]
			val := a[eqIdx+1:]
			if len(val) >= 2 && ((val[0] == '\'' && val[len(val)-1] == '\'') || (val[0] == '"' && val[len(val)-1] == '"')) {
				val = val[1 : len(val)-1]
			}
			setVar(key, val, true)
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
			unsetVar(a)
		}
		lastExitCode = 0
	case "env":
		varMu.Lock()
		var envVars []string
		for key := range exportedVars {
			if val, ok := varTable[key]; ok {
				envVars = append(envVars, key+"="+val)
			}
		}
		varMu.Unlock()
		sort.Strings(envVars)
		for _, e := range envVars {
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
	case "return":
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: return: %s: numeric argument required\n", args[1])
				lastExitCode = 2
				returnFlag = true
				return
			}
			if n < 0 {
				n = 0
			}
			if n > 255 {
				n = 255
			}
			lastExitCode = n
		} else {
			lastExitCode = 0
		}
		returnFlag = true
	case "source", ".":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "lash: source: filename argument required")
			lastExitCode = 1
			return
		}
		lastExitCode = sourceFile(args[1], cfg)
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

	resolvedArgs, extraFiles := resolveProcSubstArgs(cmdArgs)

	cmd := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = extraFiles
	}

	if inFile != "" {
		if f := resolveProcSubstFile(inFile); f != nil {
			cmd.Stdin = f
		} else {
			f, err := os.Open(inFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
				return
			}
			defer f.Close()
			cmd.Stdin = f
		}
	}

	if outFile != "" {
		if f := resolveProcSubstFile(outFile); f != nil {
			cmd.Stdout = f
		} else {
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
	}

	err := cmd.Start()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			fmt.Fprintf(os.Stderr, "lash: %s: command not found\n", resolvedArgs[0])
			lastExitCode = 127
		} else {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
		}
		return
	}

	commandStr := strings.Join(resolvedArgs, " ")

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

		resolvedArgs, extraFiles := resolveProcSubstArgs(cmdArgs)

		cmd := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stderr = os.Stderr
		if len(extraFiles) > 0 {
			cmd.ExtraFiles = extraFiles
		}

		if inFile != "" {
			if f := resolveProcSubstFile(inFile); f != nil {
				cmd.Stdin = f
			} else {
				f, err := os.Open(inFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					lastExitCode = 1
					return
				}
				defer f.Close()
				cmd.Stdin = f
			}
		} else if i == 0 {
			cmd.Stdin = os.Stdin
		} else {
			cmd.Stdin = pipes[i-1].r
		}

		if outFile != "" {
			if f := resolveProcSubstFile(outFile); f != nil {
				cmd.Stdout = f
			} else {
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

func buildEnvWithPrefix(prefix map[string]string) []string {
	varMu.Lock()
	var env []string
	seen := make(map[string]bool)
	for k, v := range prefix {
		env = append(env, k+"="+v)
		seen[k] = true
	}
	for key := range exportedVars {
		if !seen[key] {
			if val, ok := varTable[key]; ok {
				env = append(env, key+"="+val)
			}
		}
	}
	varMu.Unlock()
	return env
}

func executeSimpleWithEnv(args []string, background bool, prefixEnv map[string]string) {
	cmdArgs, inFile, outFile, appendMode := parseRedirection(args)
	if len(cmdArgs) == 0 {
		return
	}

	resolvedArgs, extraFiles := resolveProcSubstArgs(cmdArgs)

	cmd := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = buildEnvWithPrefix(prefixEnv)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = extraFiles
	}

	if inFile != "" {
		if f := resolveProcSubstFile(inFile); f != nil {
			cmd.Stdin = f
		} else {
			f, err := os.Open(inFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
				return
			}
			defer f.Close()
			cmd.Stdin = f
		}
	}

	if outFile != "" {
		if f := resolveProcSubstFile(outFile); f != nil {
			cmd.Stdout = f
		} else {
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
	}

	err := cmd.Start()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			fmt.Fprintf(os.Stderr, "lash: %s: command not found\n", resolvedArgs[0])
			lastExitCode = 127
		} else {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
		}
		return
	}

	commandStr := strings.Join(resolvedArgs, " ")

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

func executePipelineWithEnv(segments [][]string, background bool, prefixEnv map[string]string) {
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

	childEnv := buildEnvWithPrefix(prefixEnv)
	var cmds []*exec.Cmd
	var cmdArgsList [][]string
	for i, seg := range segments {
		cmdArgs, inFile, outFile, appendMode := parseRedirection(seg)
		if len(cmdArgs) == 0 {
			return
		}

		resolvedArgs, extraFiles := resolveProcSubstArgs(cmdArgs)

		cmd := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = childEnv
		cmd.Stderr = os.Stderr
		if len(extraFiles) > 0 {
			cmd.ExtraFiles = extraFiles
		}

		if inFile != "" {
			if f := resolveProcSubstFile(inFile); f != nil {
				cmd.Stdin = f
			} else {
				f, err := os.Open(inFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					lastExitCode = 1
					return
				}
				defer f.Close()
				cmd.Stdin = f
			}
		} else if i == 0 {
			cmd.Stdin = os.Stdin
		} else {
			cmd.Stdin = pipes[i-1].r
		}

		if outFile != "" {
			if f := resolveProcSubstFile(outFile); f != nil {
				cmd.Stdout = f
			} else {
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
