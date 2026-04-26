package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
var breakFlag bool
var continueFlag bool
var interruptFlag int32
var interruptSignal int32
var suspendedCont func()
var suspendedCommand string
var stdinReader *bufio.Reader
var pendingNotifs []string
var notifMu sync.Mutex
var currentConfig *Config
var globalEditor *LineEditor
var setErrExit bool
var setXTrace bool
var setPipefail bool
var setNoClobber bool
var setNoUnset bool
var setNoGlob bool
var setNotify bool
var setHistIgnoreDups bool
var setHistIgnoreSpace bool
var setHupOnExit bool
var setIgnoreEOF bool
var setHashAll bool
var setLashenv bool
var inCondition bool
var inSubshell bool
var shellInteractive bool
var shellLogin bool
var shellInitialized bool
var currentSourceFile string
var currentSourceLine int
var currentFuncName string
var callDepth int

var heredocMap map[string]*heredocInfo
var heredocCount int

type heredocInfo struct {
	Content string
	Quoted  bool
	Strip   bool
}

var varTable map[string]string
var exportedVars map[string]bool
var varMu sync.Mutex

var positionalParams []string
var scopeStack []map[string]string

var funcTable map[string]*FuncDef
var funcMu sync.Mutex

const defaultPS1 = `╭─| \l | \033[36m\u\033[0m@\033[36m\h\033[0m | \033[36m\w\033[0m \g{| \033[33m\g\033[31m\G!\033[0m }|\F─\X{❯ \X \033[31m\x↵\033[0m }❮\n╰─$ `

func initVarTable() {
	varTable = make(map[string]string)
	exportedVars = make(map[string]bool)
	funcTable = make(map[string]*FuncDef)
	heredocMap = make(map[string]*heredocInfo)
	initHashTable()
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
	for i := len(scopeStack) - 1; i >= 0; i-- {
		if val, ok := scopeStack[i][name]; ok {
			return val
		}
	}
	if val, ok := varTable[name]; ok {
		return val
	}
	envVal := os.Getenv(name)
	if setNoUnset && envVal == "" {
		fmt.Fprintf(os.Stderr, "lash: %s: unbound variable\n", name)
		expandError = true
		return ""
	}
	return envVal
}

func getVarQuiet(name string) string {
	varMu.Lock()
	defer varMu.Unlock()
	for i := len(scopeStack) - 1; i >= 0; i-- {
		if val, ok := scopeStack[i][name]; ok {
			return val
		}
	}
	if val, ok := varTable[name]; ok {
		return val
	}
	return os.Getenv(name)
}

func getVarLocked(name string) (string, bool) {
	for i := len(scopeStack) - 1; i >= 0; i-- {
		if val, ok := scopeStack[i][name]; ok {
			return val, true
		}
	}
	val, ok := varTable[name]
	return val, ok
}

func setVar(name, value string, exported bool) {
	varMu.Lock()
	defer varMu.Unlock()
	inScope := false
	for i := len(scopeStack) - 1; i >= 0; i-- {
		if _, ok := scopeStack[i][name]; ok {
			scopeStack[i][name] = value
			inScope = true
			break
		}
	}
	if !inScope {
		varTable[name] = value
	}
	if exported {
		exportedVars[name] = true
		os.Setenv(name, value)
	} else if exportedVars[name] {
		os.Setenv(name, value)
	}
}

func unsetVar(name string) {
	varMu.Lock()
	defer varMu.Unlock()
	for i := len(scopeStack) - 1; i >= 0; i-- {
		if _, ok := scopeStack[i][name]; ok {
			delete(scopeStack[i], name)
			return
		}
	}
	delete(varTable, name)
	delete(exportedVars, name)
	os.Unsetenv(name)
}

func isExported(name string) bool {
	varMu.Lock()
	defer varMu.Unlock()
	return exportedVars[name]
}

func pushScope() {
	scopeStack = append(scopeStack, make(map[string]string))
}

func popScope() {
	if len(scopeStack) > 0 {
		scopeStack = scopeStack[:len(scopeStack)-1]
	}
}

func setLocal(name, value string) bool {
	if len(scopeStack) == 0 {
		return false
	}
	scopeStack[len(scopeStack)-1][name] = value
	return true
}

func defineFunc(name string, def *FuncDef) {
	funcMu.Lock()
	defer funcMu.Unlock()
	funcTable[name] = def
}

func lookupFunc(name string) *FuncDef {
	funcMu.Lock()
	defer funcMu.Unlock()
	if fn, ok := funcTable[name]; ok {
		return fn
	}
	return nil
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

func getPS2() string {
	ps2 := getVar("PS2")
	if ps2 == "" {
		ps2 = "> "
	}
	return expandPS1(ps2)
}

func getPS4() string {
	ps4 := getVar("PS4")
	if ps4 == "" {
		ps4 = "+ "
	}
	expanded := expandPS1(ps4)
	expanded = expandPS4Escapes(expanded)
	return expanded
}

func expandPS4Escapes(s string) string {
	var b strings.Builder
	runes := []rune(s)
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
		case 's':
			if currentSourceFile != "" {
				b.WriteString(currentSourceFile)
			}
		case 'S':
			if currentFuncName != "" {
				b.WriteString(currentFuncName)
			} else if currentSourceFile != "" {
				b.WriteString(currentSourceFile)
			}
		case 'N':
			b.WriteString(strconv.Itoa(callDepth))
		case 'L':
			b.WriteString(strconv.Itoa(currentSourceLine))
		default:
			b.WriteByte('\\')
			b.WriteRune(runes[i])
		}
	}
	return b.String()
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
				b.WriteString(fmt.Sprintf("%d", lastExitCode))
			}
		case 'l':
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				if icon := getNamedIcon(content); icon != "" {
					b.WriteString(icon)
				} else {
					b.WriteString(getOSIcon())
				}
				i += consumed
			} else {
				b.WriteString(getOSIcon())
			}
		case 'c':
			if content, consumed, ok := tryParseBrace(runes, i+1); ok {
				expandedContent := expandPS1Escapes(content)
				contentWidth := visibleWidth(expandedContent)
				currentLeft := b.String()
				lastNl := strings.LastIndex(currentLeft, "\n")
				var lastLine string
				if lastNl >= 0 {
					lastLine = currentLeft[lastNl+1:]
				} else {
					lastLine = currentLeft
				}
				stripped := strings.TrimRight(lastLine, " ")
				if len(stripped) != len(lastLine) {
					prefix := ""
					if lastNl >= 0 {
						prefix = currentLeft[:lastNl+1]
					}
					b.Reset()
					b.WriteString(prefix)
					b.WriteString(stripped)
				}
				leftWidth := visibleWidth(stripped)
				termWidth := getTermWidth()
				availableWidth := termWidth - leftWidth
				if contentWidth > availableWidth {
					b.WriteString(expandedContent)
				} else {
					padding := (availableWidth - contentWidth) / 2
					b.WriteString(strings.Repeat(" ", padding))
					b.WriteString(expandedContent)
				}
				i += consumed
			} else {
				b.WriteByte('\\')
				b.WriteRune(runes[i])
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
	ctx := defaultContext()

	var allLines []string
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}

	lineIdx := 0
	readNextFromSource := func() (string, error) {
		if lineIdx >= len(allLines) {
			return "", io.EOF
		}
		line := allLines[lineIdx]
		lineIdx++
		return line + "\n", nil
	}

	savedSourceFile := currentSourceFile
	savedSourceLine := currentSourceLine
	currentSourceFile = path
	for lineIdx < len(allLines) {
		currentSourceLine = lineIdx + 1
		line := allLines[lineIdx]
		lineIdx++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		tokens := tokenize(trimmed)
		if len(tokens) > 0 && (tokens[0] == "alias" || tokens[0] == "unalias" || tokens[0] == "fetch") {
			executeBuiltin(tokens, ctx)
			continue
		}

		processed := preprocessHeredocs(trimmed, readNextFromSource)
		processed = expandAliasLine(processed)
		if processed == "" {
			continue
		}
		returnFlag = false
		prog := Parse(processed)
		executeNode(prog, ctx)
	}

	currentSourceFile = savedSourceFile
	currentSourceLine = savedSourceLine
	return lastExitCode
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

func sourceIfExists(path string, cfg *Config) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	f.Close()
	sourceFile(path, cfg)
}

func sourceProfile(cfg *Config) {
	// /etc/lash_profile — system-wide login profile, does not exist by default
	sourceIfExists("/etc/lash_profile", cfg)

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	sourceIfExists(filepath.Join(home, ".lash_profile"), cfg)
}

func preprocessHeredocs(line string, readNextLine func() (string, error)) string {
	tokens := tokenize(line)
	var result strings.Builder
	i := 0
	for i < len(tokens) {
		t := tokens[i]
		if (t == "<<" || t == "<<-") && i+1 < len(tokens) {
			op := t
			delimToken := tokens[i+1]
			strip := op == "<<-"

			quoted := false
			delim := delimToken
			if len(delim) >= 2 && ((delim[0] == '\'' && delim[len(delim)-1] == '\'') || (delim[0] == '"' && delim[len(delim)-1] == '"')) {
				quoted = true
				delim = delim[1 : len(delim)-1]
			}

			placeholder := "__LASH_HEREDOC_" + strconv.Itoa(heredocCount) + "__"
			heredocCount++

			var content strings.Builder
			for {
				nextLine, err := readNextLine()
				if err != nil {
					break
				}
				trimmed := strings.TrimRight(nextLine, "\r\n")
				if strip {
					trimmed = strings.TrimLeft(trimmed, "\t")
				}
				if trimmed == delim {
					break
				}
				content.WriteString(nextLine)
			}

			heredocMap[placeholder] = &heredocInfo{
				Content: content.String(),
				Quoted:  quoted,
				Strip:   strip,
			}

			result.WriteString(op)
			result.WriteByte(' ')
			result.WriteString(placeholder)
			i += 2
			continue
		}
		result.WriteString(t)
		if i+1 < len(tokens) {
			result.WriteByte(' ')
		}
		i++
	}
	return result.String()
}

func shellExit(code int) {
	if setHupOnExit && shellInitialized {
		jobMu.Lock()
		var pids []int
		for _, j := range jobTable {
			if j.State == JobRunning || j.State == JobStopped {
				pids = append(pids, j.PGID)
			}
		}
		jobMu.Unlock()
		if len(pids) > 0 {
			for _, pgid := range pids {
				syscall.Kill(-pgid, syscall.SIGHUP)
			}
			time.Sleep(time.Minute)
			for _, pgid := range pids {
				syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	}
	os.Exit(code)
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
			printConfigList()
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
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "lash: unknown subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "see 'lash help' for shell usage.")
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: lash [options] [command]")
	fmt.Fprintln(os.Stderr, "       lash [version|set-config|theme|keybind|env ...]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "options:")
	fmt.Fprintln(os.Stderr, "  exec <string>  execute <string> and exit")
	fmt.Fprintln(os.Stderr, "  login          start as a login shell")
	fmt.Fprintln(os.Stderr, "  norc           do not source rc files")
	fmt.Fprintln(os.Stderr, "  help           show this help")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  version                    show version info")
	fmt.Fprintln(os.Stderr, "  set-config <key> <value>   set a config option")
	fmt.Fprintln(os.Stderr, "  theme <set|save|list|delete>  manage prompt themes")
	fmt.Fprintln(os.Stderr, "  keybind <set|list|reset|delete|actions>  manage key bindings")
	fmt.Fprintln(os.Stderr, "  env <refresh|allow|deny|trusted>  manage .lashenv trust")
}

func initShell() *Config {
	shellInteractive = isTerminal()
	shellLogin = false
	norc := false
	var cmdString string
	var themeArgs []string
	var keybindArgs []string
	var envArgs []string

	args := os.Args[1:]
	i := 0
	for i < len(args) {
		switch args[i] {
		case "help":
			printUsage()
			os.Exit(0)
		case "login":
			shellLogin = true
			i++
		case "norc":
			norc = true
			i++
		case "exec":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "lash: exec: option requires an argument")
				fmt.Fprintln(os.Stderr, "see 'lash help' for shell usage.")
				os.Exit(2)
			}
			cmdString = args[i]
			i++
		case "theme":
			themeArgs = args[i:]
			i = len(args)
		case "keybind":
			keybindArgs = args[i:]
			i = len(args)
		case "env":
			envArgs = args[i:]
			i = len(args)
		default:
			if !strings.HasPrefix(args[i], "-") {
				// skip if it looks like a file path (e.g. Android/termux passing binary path as arg)
				if strings.Contains(args[i], "/") {
					i++
					continue
				}
				handleGlobalCommand(args[i:])
				return nil
			}
			fmt.Fprintf(os.Stderr, "lash: unknown option: %s\n", args[i])
			fmt.Fprintln(os.Stderr, "see 'lash help' for shell usage.")
			os.Exit(2)
		}
	}

	if !shellLogin {
		argv0 := filepath.Base(os.Args[0])
		if strings.HasPrefix(argv0, "-") {
			shellLogin = true
		}
	}

	initJobControl()
	initTrapTable()
	initAliases()
	initVarTable()
	initArrayTable()
	initOptions()
	os.Setenv("PS1", defaultPS1)
	setVar("PS1", defaultPS1, true)

	if shellInteractive {
		os.Setenv("LASH_INTERACTIVE", "1")
	}
	if shellLogin {
		os.Setenv("LASH_LOGIN", "1")
	}

	cfg := LoadConfig()
	currentConfig = cfg
	applyConfigToOptions(cfg)
	initColoredOutput(cfg)

	if shellLogin {
		sourceProfile(cfg)
	}
	if shellInteractive && !norc {
		sourceLashrc(cfg)
	}

	if len(keybindArgs) > 0 {
		if len(keybindArgs) < 2 {
			printKeybindHelp()
			os.Exit(1)
		}
		switch keybindArgs[1] {
		case "set":
			builtinKeybindSet(keybindArgs[2:])
		case "list":
			builtinKeybindList()
		case "reset":
			builtinKeybindReset(keybindArgs[2:])
		case "delete":
			builtinKeybindDelete(keybindArgs[2:])
		case "actions":
			builtinKeybindActions()
		case "help":
			printKeybindHelp()
		default:
			fmt.Fprintf(os.Stderr, "lash: keybind: unknown subcommand: %s\n", keybindArgs[1])
			fmt.Fprintln(os.Stderr, "see 'lash keybind help' for keybind usage.")
			lastExitCode = 1
		}
		os.Exit(lastExitCode)
	}

	if len(themeArgs) > 0 {
		if len(themeArgs) < 2 {
			printThemeHelp()
			os.Exit(1)
		}
		ctx := defaultContext()
		switch themeArgs[1] {
		case "set":
			builtinThemeSet(themeArgs[2:], ctx)
		case "save":
			builtinThemeSave(themeArgs[2:])
		case "list":
			builtinThemeList()
		case "delete":
			builtinThemeDelete(themeArgs[2:])
		case "help":
			printThemeHelp()
		default:
			fmt.Fprintf(os.Stderr, "lash: theme: unknown subcommand: %s\n", themeArgs[1])
			fmt.Fprintln(os.Stderr, "see 'lash theme help' for theme usage.")
			lastExitCode = 1
		}
		shellExit(lastExitCode)
	}

	if len(envArgs) > 0 {
		if len(envArgs) < 2 {
			printEnvHelp()
			os.Exit(1)
		}
		cfg := LoadConfig()
		currentConfig = cfg
		applyConfigToOptions(cfg)
		switch envArgs[1] {
		case "refresh":
			builtinEnvRefresh(cfg)
		case "allow":
			builtinEnvAllow(envArgs[2:])
		case "deny":
			builtinEnvDeny(envArgs[2:])
		case "trusted":
			builtinEnvTrusted()
		case "help":
			printEnvHelp()
		default:
			fmt.Fprintf(os.Stderr, "lash: env: unknown subcommand: %s\n", envArgs[1])
			fmt.Fprintln(os.Stderr, "see 'lash env help' for env usage.")
			lastExitCode = 1
		}
		os.Exit(lastExitCode)
	}

	if !norc && cfg.Theme != "" {
		sourceIfExists(themesDirPath()+"/"+cfg.Theme, cfg)
	}

	if cmdString != "" {
		stdinReader = bufio.NewReader(os.Stdin)
		returnFlag = false
		currentSourceLine = 1
		prog := Parse(cmdString)
		ctx := defaultContext()
		executeNode(prog, ctx)
		shellExit(lastExitCode)
	}

	return cfg
}

func main() {
	cfg := initShell()
	if cfg == nil {
		return
	}
	shellInitialized = true

	if setLashenv {
		tryLoadLashenv(cfg)
	}

	globalEditor = NewLineEditor(cfg)
	stdinReader = bufio.NewReader(os.Stdin)
	for {
		reapZombies()
		drainNotifs()
		prompt := getPrompt()
		line, err := globalEditor.ReadLine(prompt)
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

		for {
			tks := tokenize(line)
			if len(tks) == 0 {
				break
			}
			last := tks[len(tks)-1]
			if last != "|" && last != ">" && last != ">>" && last != ">|" && last != "<" && last != "<<" && last != "<<-" && last != "<<<" && last != ";" && last != "&&" && last != "||" {
				break
			}
			globalEditor.continuation = true
			nextLine, err := globalEditor.ReadLine(getPS2())
			globalEditor.continuation = false
			if err != nil {
				break
			}
			if nextLine == "\x03" {
				lastExitCode = 130
				line = ""
				break
			}
			if nextLine == "" {
				break
			}
			line = line + " " + nextLine
		}
		if line == "" {
			continue
		}

		line = preprocessHeredocs(line, func() (string, error) {
			globalEditor.continuation = true
			nextLine, err := globalEditor.ReadLine(getPS2())
			globalEditor.continuation = false
			if err != nil {
				return "", err
			}
			return nextLine + "\n", nil
		})

		tokens := tokenize(line)
		if len(tokens) > 0 && (tokens[0] == "alias" || tokens[0] == "unalias" || tokens[0] == "fetch") {
			executeBuiltin(tokens, defaultContext())
			continue
		}

		returnFlag = false
		interruptFlag = 0
		if !strings.HasPrefix(line, "fg") {
			suspendedCont = nil
			suspendedCommand = ""
		}
		cmdNumber++
		currentSourceLine = cmdNumber
		prog := Parse(line)
		executeNode(prog, defaultContext())
	}
}
