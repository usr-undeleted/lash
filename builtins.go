package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var allBuiltins = []string{
	"exit", "cd", "pwd", "jobs", "fg", "bg", "kill", "export", "lash",
	"echo", "true", "false", "unset", "env", "type", "which", "alias", "unalias",
	"source", ".", "return", "local", "shift", "read", "set", "fetch",
}

func isBuiltin(cmd string) bool {
	for _, b := range allBuiltins {
		if b == cmd {
			return true
		}
	}
	return false
}

func executeBuiltin(args []string, ctx *ExecContext) {
	switch args[0] {
	case "exit":
		code := lastExitCode
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err == nil {
				code = n
			}
		}
		if inSubshell {
			lastExitCode = code
			returnFlag = true
			return
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
				printConfigList()
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
			if !ctx.Cfg.Set(key, val) {
				fmt.Fprintf(os.Stderr, "lash: unknown config key: %s\n", key)
				lastExitCode = 1
				return
			}
			if err := ctx.Cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "lash: failed to save config: %s\n", err)
				lastExitCode = 1
				return
			}
			fmt.Printf("lash: set %s = %s\n", key, val)
			lastExitCode = 0
		case "version":
			printVersion(ctx.Cfg.LogoSize)
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
		unsetFunc := false
		var varNames []string
		for _, a := range args[1:] {
			if a == "-f" {
				unsetFunc = true
				continue
			}
			if a == "-v" {
				continue
			}
			varNames = append(varNames, a)
		}
		if len(varNames) == 0 {
			fmt.Fprintln(os.Stderr, "unset: usage: unset [-f] [-v] <name> [name...]")
			lastExitCode = 1
			return
		}
		for _, a := range varNames {
			if unsetFunc {
				funcMu.Lock()
				delete(funcTable, a)
				funcMu.Unlock()
			} else {
				unsetVar(a)
			}
		}
		lastExitCode = 0
	case "set":
		if len(args) == 1 {
			varMu.Lock()
			var keys []string
			for k := range varTable {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Printf("%s=%s\n", k, varTable[k])
			}
			varMu.Unlock()
			lastExitCode = 0
			return
		}
		i := 1
		for i < len(args) && len(args[i]) > 0 && (args[i][0] == '-' || args[i][0] == '+') {
			if len(args[i]) < 2 {
				break
			}
			enable := args[i][0] == '-'
			flag := args[i][1:]
			switch flag {
			case "e":
				setErrExit = enable
			case "x":
				setXTrace = enable
			case "-":
				i++
				positionalParams = args[i:]
				lastExitCode = 0
				return
			case "o":
				i++
				if i >= len(args) {
					fmt.Fprintln(os.Stderr, "set: -o: option requires an argument")
					lastExitCode = 2
					return
				}
				opt := args[i]
				switch opt {
				case "pipefail":
					setPipefail = enable
				default:
					fmt.Fprintf(os.Stderr, "set: -o: %s: invalid option\n", opt)
					lastExitCode = 2
					return
				}
			default:
				if enable {
					positionalParams = args[i:]
					lastExitCode = 0
					return
				}
				fmt.Fprintf(os.Stderr, "set: %s: invalid option\n", args[i])
				lastExitCode = 2
				return
			}
			i++
		}
		if i < len(args) {
			positionalParams = args[i:]
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
			} else if isAlias(a) {
				aliasMu.RLock()
				fmt.Printf("%s is aliased to '%s'\n", a, aliasTable[a].Raw)
				aliasMu.RUnlock()
			} else if lookupFunc(a) != nil {
				fmt.Printf("%s is a function\n", a)
			} else if isKeyword(a) {
				fmt.Printf("%s is a shell keyword\n", a)
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
	case "local":
		if len(scopeStack) == 0 {
			fmt.Fprintln(os.Stderr, "local: can only be used in a function")
			lastExitCode = 1
			return
		}
		for _, a := range args[1:] {
			eqIdx := strings.Index(a, "=")
			if eqIdx < 0 {
				if !setLocal(a, "") {
					lastExitCode = 1
					return
				}
			} else {
				name := a[:eqIdx]
				val := a[eqIdx+1:]
				if !setLocal(name, val) {
					lastExitCode = 1
					return
				}
			}
		}
		lastExitCode = 0
	case "shift":
		n := 1
		if len(args) > 1 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "shift: %s: numeric argument required\n", args[1])
				lastExitCode = 1
				return
			}
			n = parsed
		}
		if n < 0 {
			n = 0
		}
		if n > len(positionalParams) {
			fmt.Fprintln(os.Stderr, "shift: shift count out of range")
			lastExitCode = 1
			return
		}
		positionalParams = positionalParams[n:]
		lastExitCode = 0
	case "source", ".":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "lash: source: filename argument required")
			lastExitCode = 1
			return
		}
		lastExitCode = sourceFile(args[1], ctx.Cfg)
	case "read":
		prompt := ""
		raw := false
		maxChars := 0
		timeout := 0
		delim := "\n"
		var varNames []string

		i := 1
		for i < len(args) && len(args[i]) > 0 && args[i][0] == '-' {
			switch args[i] {
			case "-p":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "lash: read: -p: option requires an argument")
					lastExitCode = 2
					return
				}
				i++
				prompt = args[i]
			case "-r":
				raw = true
			case "-s":
			case "-n":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "lash: read: -n: option requires an argument")
					lastExitCode = 2
					return
				}
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 0 {
					fmt.Fprintf(os.Stderr, "lash: read: %s: invalid number\n", args[i])
					lastExitCode = 2
					return
				}
				maxChars = n
			case "-t":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "lash: read: -t: option requires an argument")
					lastExitCode = 2
					return
				}
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 0 {
					fmt.Fprintf(os.Stderr, "lash: read: %s: invalid timeout\n", args[i])
					lastExitCode = 2
					return
				}
				timeout = n
			case "-d":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "lash: read: -d: option requires an argument")
					lastExitCode = 2
					return
				}
				i++
				delim = args[i]
			default:
				fmt.Fprintf(os.Stderr, "lash: read: %s: invalid option\n", args[i])
				lastExitCode = 2
				return
			}
			i++
		}
		varNames = args[i:]

		var reader *bufio.Reader
		if stdinReader != nil {
			reader = stdinReader
		} else {
			reader = bufio.NewReader(ctx.Stdin)
		}

		if prompt != "" {
			fmt.Fprintf(ctx.Stderr, "%s", prompt)
		}

		var input string
		var readErr error

		if timeout > 0 {
			type readResult struct {
				data string
				err  error
			}
			ch := make(chan readResult, 1)
			go func() {
				var line string
				var err error
				if maxChars > 0 {
					buf := make([]byte, maxChars)
					n, rerr := io.ReadFull(reader, buf)
					if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
						ch <- readResult{"", rerr}
						return
					}
					line = string(buf[:n])
				} else {
					if len(delim) == 1 && delim[0] != '\n' {
						line, err = reader.ReadString(delim[0])
					} else {
						line, err = reader.ReadString('\n')
					}
				}
				ch <- readResult{line, err}
			}()
			select {
			case res := <-ch:
				input = res.data
				readErr = res.err
			case <-time.After(time.Duration(timeout) * time.Second):
				lastExitCode = 142
				return
			}
		} else {
			if maxChars > 0 {
				buf := make([]byte, maxChars)
				n, rerr := io.ReadFull(reader, buf)
				if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
					lastExitCode = 1
					return
				}
				input = string(buf[:n])
			} else {
				if len(delim) == 1 && delim[0] != '\n' {
					input, readErr = reader.ReadString(delim[0])
				} else {
					input, readErr = reader.ReadString('\n')
				}
			}
		}

		if readErr != nil {
			if readErr == io.EOF && len(input) > 0 {
				setVar("REPLY", input, false)
				assignReadVars(varNames, input)
				lastExitCode = 0
				return
			}
			lastExitCode = 1
			return
		}

		input = strings.TrimRight(input, "\r\n")
		if len(delim) == 1 && delim[0] != '\n' {
			input = strings.TrimRight(input, delim)
		}

		if !raw {
			input = processReadEscapes(reader, input)
		}

		setVar("REPLY", input, false)
		assignReadVars(varNames, input)
		lastExitCode = 0
	case "fetch":
		builtinFetch(args, ctx)
	}
}

func assignReadVars(varNames []string, input string) {
	ifs := getVar("IFS")
	if ifs == "" {
		ifs = " \t\n"
	}

	if len(varNames) == 0 {
		return
	}

	if len(varNames) == 1 {
		setVar(varNames[0], input, false)
		return
	}

	trimmed := strings.TrimRight(input, ifs)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		for _, name := range varNames {
			setVar(name, "", false)
		}
		return
	}

	for i := 0; i < len(varNames)-1; i++ {
		if i < len(fields) {
			setVar(varNames[i], fields[i], false)
		} else {
			setVar(varNames[i], "", false)
		}
	}

	lastVar := varNames[len(varNames)-1]
	if len(fields) < len(varNames) {
		setVar(lastVar, "", false)
	} else {
		remainder := strings.Join(fields[len(varNames)-1:], " ")
		setVar(lastVar, remainder, false)
	}
}

func processReadEscapes(reader *bufio.Reader, line string) string {
	var result strings.Builder
	i := 0
	for i < len(line) {
		if line[i] == '\\' && i+1 < len(line) {
			next := line[i+1]
			if next == '\n' {
				nextLine, err := reader.ReadString('\n')
				if err != nil {
					i += 2
					continue
				}
				nextLine = strings.TrimRight(nextLine, "\r\n")
				if i > 0 || result.Len() > 0 {
					result.WriteByte(' ')
				}
				line = nextLine
				i = 0
				continue
			}
			result.WriteByte(next)
			i += 2
			continue
		}
		result.WriteByte(line[i])
		i++
	}
	return result.String()
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
