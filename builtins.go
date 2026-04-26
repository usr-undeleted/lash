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
	"source", ".", "return", "local", "shift", "read", "set", "fetch", "trap",
	"test", "[", "declare", "mapfile", "readarray", "hash",
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
		shellExit(code)
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
			if setLashenv {
				tryLoadLashenv(ctx.Cfg)
			}
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
		cont := suspendedCont
		if cont != nil {
			job := getMostRecentJob()
			if job != nil && job.State == JobStopped {
				suspendedCont = nil
				setFgPIDs([]int{job.PID})
				markJobRunningByPID(job.PID)
				exitCode := waitForeground([]int{job.PID}, job.PGID, job.Command)
				clearFgPIDs()
				if exitCode < 0 {
					lastExitCode = 128 + int(syscall.SIGTSTP)
				} else {
					lastExitCode = exitCode
				}
				signalInterruptFromExitCode(lastExitCode)
				cont()
			} else {
				suspendedCont = nil
				lastExitCode = 0
				cont()
			}
		} else {
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
				fmt.Printf("%2d) %s\n", sig, signalName(sig))
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
					os.Setenv(a, getVarQuiet(a))
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
			if key == "PATH" {
				hashClear()
			}
			lastExitCode = 0
		}
	case "lash":
		if len(args) < 2 {
			bin, err := os.Executable()
			if err != nil {
				bin = os.Args[0]
			}
			cmd := exec.Command(bin)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = os.Environ()
			err = cmd.Run()
			if err != nil {
				lastExitCode = 1
			}
			return
		}
		switch args[1] {
		case "set-config":
			if len(args) == 3 && args[2] == "list" {
				printConfigList()
				lastExitCode = 0
				return
			}
			if len(args) == 3 && args[2] == "show" {
				printConfigShow(ctx.Cfg)
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
			applyConfigToOptions(ctx.Cfg)
			if err := ctx.Cfg.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "lash: failed to save config: %s\n", err)
				lastExitCode = 1
				return
			}
			lastExitCode = 0
		case "version":
			printVersion(ctx.Cfg.LogoSize)
			lastExitCode = 0
		case "help":
			printUsage()
			lastExitCode = 0
		case "theme":
			if len(args) < 3 {
				printThemeHelp()
				lastExitCode = 1
				return
			}
			switch args[2] {
			case "set":
				builtinThemeSet(args[3:], ctx)
			case "save":
				builtinThemeSave(args[3:])
			case "list":
				builtinThemeList()
			case "delete":
				builtinThemeDelete(args[3:])
			case "help":
				printThemeHelp()
				lastExitCode = 0
			default:
				fmt.Fprintf(os.Stderr, "lash: theme: unknown subcommand: %s\n", args[2])
				fmt.Fprintln(os.Stderr, "see 'lash theme help' for theme usage.")
				lastExitCode = 1
			}
		case "keybind":
			if len(args) < 3 {
				printKeybindHelp()
				lastExitCode = 1
				return
			}
			switch args[2] {
			case "set":
				builtinKeybindSet(args[3:])
			case "list":
				builtinKeybindList()
				lastExitCode = 0
			case "reset":
				builtinKeybindReset(args[3:])
			case "delete":
				builtinKeybindDelete(args[3:])
			case "actions":
				builtinKeybindActions()
				lastExitCode = 0
			case "help":
				printKeybindHelp()
				lastExitCode = 0
			default:
				fmt.Fprintf(os.Stderr, "lash: keybind: unknown subcommand: %s\n", args[2])
				fmt.Fprintln(os.Stderr, "see 'lash keybind help' for keybind usage.")
				lastExitCode = 1
			}
		case "env":
			if len(args) < 3 {
				printEnvHelp()
				lastExitCode = 1
				return
			}
			switch args[2] {
			case "refresh":
				builtinEnvRefresh(ctx.Cfg)
			case "allow":
				builtinEnvAllow(args[3:])
			case "deny":
				builtinEnvDeny(args[3:])
			case "trusted":
				builtinEnvTrusted()
			case "help":
				printEnvHelp()
				lastExitCode = 0
			default:
				fmt.Fprintf(os.Stderr, "lash: env: unknown subcommand: %s\n", args[2])
				fmt.Fprintln(os.Stderr, "see 'lash env help' for env usage.")
				lastExitCode = 1
			}
		default:
			fmt.Fprintf(os.Stderr, "lash: unknown subcommand: %s\n", args[1])
			fmt.Fprintln(os.Stderr, "see 'lash help' for shell usage.")
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
			ctx.Stdout.Write([]byte(output))
		} else {
			ctx.Stdout.Write([]byte(output + "\n"))
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
				bracketIdx := strings.Index(a, "[")
				if bracketIdx >= 0 && strings.HasSuffix(a, "]") {
					arrName := a[:bracketIdx]
					unsetArray(arrName)
				} else if isArray(a) {
					unsetArray(a)
				} else {
					unsetVar(a)
				}
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
			var arrKeys []string
			for k := range arrayTable {
				arrKeys = append(arrKeys, k)
			}
			sort.Strings(arrKeys)
			for _, k := range arrKeys {
				arr := arrayTable[k]
				if arr.IsAssoc {
					akeys := make([]string, 0, len(arr.Assoc))
					for ak := range arr.Assoc {
						akeys = append(akeys, ak)
					}
					sort.Strings(akeys)
					for _, ak := range akeys {
						fmt.Printf("%s[%s]=%s\n", k, ak, arr.Assoc[ak])
					}
				} else {
					for idx, v := range arr.Indexed {
						fmt.Printf("%s[%d]=%s\n", k, idx, v)
					}
				}
			}
			lastExitCode = 0
			return
		}
		i := 1
		for i < len(args) && len(args[i]) > 0 && args[i] == "--" {
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
			} else if isArray(a) {
				fmt.Printf("%s is an array\n", a)
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
				bracketIdx := strings.Index(a, "[")
				if bracketIdx >= 0 && strings.HasSuffix(a, "]") {
					arrName := a[:bracketIdx]
					if len(arrayScopeStack) > 0 {
						arr := getArray(arrName)
						if arr == nil {
							arr = &ArrayVar{Indexed: []string{}, IsIndexed: true}
						}
						arrayScopeStack[len(arrayScopeStack)-1][arrName] = arr
					}
					continue
				}
				if isArray(a) {
					if len(arrayScopeStack) > 0 {
						arrayScopeStack[len(arrayScopeStack)-1][a] = getArray(a)
					}
					continue
				}
				if len(arrayScopeStack) > 0 {
					arrayScopeStack[len(arrayScopeStack)-1][a] = nil
				}
				if !setLocal(a, "") {
					lastExitCode = 1
					return
				}
			} else {
				name := a[:eqIdx]
				val := a[eqIdx+1:]
				if val == "(" || (len(val) > 0 && val[0] == '(') {
					continue
				}
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
		readArray := ""
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
			case "-a":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "lash: read: -a: option requires an argument")
					lastExitCode = 2
					return
				}
				i++
				readArray = args[i]
			default:
				fmt.Fprintf(os.Stderr, "lash: read: %s: invalid option\n", args[i])
				lastExitCode = 2
				return
			}
			i++
		}
		varNames = args[i:]

		var reader *bufio.Reader
		if ctx.Stdin != os.Stdin {
			reader = bufio.NewReader(ctx.Stdin)
		} else if stdinReader != nil {
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
				if readArray != "" {
					fields := strings.Fields(input)
					arr := &ArrayVar{Indexed: fields, IsIndexed: true}
					setArray(readArray, arr)
				} else {
					assignReadVars(varNames, input)
				}
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
		if readArray != "" {
			ifs := getVar("IFS")
			if ifs == "" {
				ifs = " \t\n"
			}
			fields := strings.Fields(input)
			arr := &ArrayVar{Indexed: fields, IsIndexed: true}
			setArray(readArray, arr)
		} else {
			assignReadVars(varNames, input)
		}
		lastExitCode = 0
	case "fetch":
		builtinFetch(args, ctx)
	case "test", "[":
		builtinTest(args)
	case "trap":
		if len(args) == 1 {
			printTrapHandlers(nil)
			lastExitCode = 0
			return
		}
		if args[1] == "-l" {
			for _, sig := range listSignals() {
				fmt.Printf("%2d) %s\n", sig, signalName(sig))
			}
			lastExitCode = 0
			return
		}
		if args[1] == "-p" {
			var sigs []syscall.Signal
			for _, a := range args[2:] {
				s, err := parseSignal(a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "trap: %s: %s\n", a, err)
					lastExitCode = 1
					return
				}
				sigs = append(sigs, s)
			}
			printTrapHandlers(sigs)
			lastExitCode = 0
			return
		}
		if args[1] == "-" && len(args) >= 3 {
			for _, a := range args[2:] {
				s, err := parseSignal(a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "trap: %s: %s\n", a, err)
					lastExitCode = 1
					continue
				}
				if s == syscall.SIGKILL || s == syscall.SIGSTOP {
					fmt.Fprintf(os.Stderr, "trap: %s: cannot trap\n", a)
					lastExitCode = 1
					continue
				}
				clearTrap(s)
			}
			lastExitCode = 0
			return
		}
		handler := args[1]
		sigArgs := args[2:]
		if len(sigArgs) == 0 {
			s, err := parseSignal(handler)
			if err == nil {
				h := getTrap(s)
				if h == "" {
					fmt.Fprintf(os.Stderr, "trap: %s: not trapped\n", handler)
					lastExitCode = 1
				} else {
					fmt.Printf("trap -- '%s' %s\n", h, signalName(s))
					lastExitCode = 0
				}
				return
			}
			fmt.Fprintf(os.Stderr, "trap: usage: trap [-lp] [handler signal ...]\n")
			lastExitCode = 2
			return
		}
		for _, a := range sigArgs {
			s, err := parseSignal(a)
			if err != nil {
				fmt.Fprintf(os.Stderr, "trap: %s: %s\n", a, err)
				lastExitCode = 1
				continue
			}
			if s == syscall.SIGKILL || s == syscall.SIGSTOP {
				fmt.Fprintf(os.Stderr, "trap: %s: cannot trap\n", a)
				lastExitCode = 1
				continue
			}
			setTrap(s, handler)
		}
		lastExitCode = 0
	case "declare":
		builtinDeclare(args)
	case "mapfile", "readarray":
		builtinMapfile(args, ctx)
	case "hash":
		builtinHash(args)
	}
}

func printTrapHandlers(sigs []syscall.Signal) {
	trapMu.RLock()
	defer trapMu.RUnlock()
	var ordered []syscall.Signal
	if len(sigs) > 0 {
		ordered = sigs
	} else {
		for s := range trapTable {
			ordered = append(ordered, s)
		}
		sort.Slice(ordered, func(i, j int) bool { return int(ordered[i]) < int(ordered[j]) })
	}
	for _, s := range ordered {
		h, ok := trapTable[s]
		if !ok {
			continue
		}
		fmt.Printf("trap -- '%s' %s\n", h, signalName(s))
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

func builtinDeclare(args []string) {
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
		var arrKeys []string
		for k := range arrayTable {
			arrKeys = append(arrKeys, k)
		}
		sort.Strings(arrKeys)
		for _, k := range arrKeys {
			arr := arrayTable[k]
			if arr.IsAssoc {
				fmt.Printf("declare -A %s\n", k)
				akeys := make([]string, 0, len(arr.Assoc))
				for ak := range arr.Assoc {
					akeys = append(akeys, ak)
				}
				sort.Strings(akeys)
				for _, ak := range akeys {
					fmt.Printf("%s[%s]=%s\n", k, ak, arr.Assoc[ak])
				}
			} else {
				fmt.Printf("declare -a %s\n", k)
				for idx, v := range arr.Indexed {
					fmt.Printf("%s[%d]=%s\n", k, idx, v)
				}
			}
		}
		lastExitCode = 0
		return
	}

	isAssoc := false
	isIndexed := false
	i := 1
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		switch args[i] {
		case "-a":
			isIndexed = true
		case "-A":
			isAssoc = true
		default:
			fmt.Fprintf(os.Stderr, "declare: %s: invalid option\n", args[i])
			lastExitCode = 2
			return
		}
		i++
	}

	for ; i < len(args); i++ {
		a := args[i]
		eqIdx := strings.Index(a, "=")
		bracketIdx := strings.Index(a, "[")

		if bracketIdx >= 0 && (eqIdx < 0 || bracketIdx < eqIdx) {
			closeBracket := strings.Index(a[bracketIdx:], "]")
			if closeBracket < 0 {
				fmt.Fprintf(os.Stderr, "declare: %s: bad array subscript\n", a)
				lastExitCode = 1
				continue
			}
			name := a[:bracketIdx]
			index := a[bracketIdx+1 : bracketIdx+closeBracket]
			var val string
			if bracketIdx+closeBracket+1 < len(a) && a[bracketIdx+closeBracket+1] == '=' {
				val = a[bracketIdx+closeBracket+2:]
			}
			if !isValidVarName(name) {
				fmt.Fprintf(os.Stderr, "declare: %s: not a valid identifier\n", name)
				lastExitCode = 1
				continue
			}
			if isAssoc {
				arr := getArray(name)
				if arr == nil {
					arr = &ArrayVar{Assoc: make(map[string]string), IsAssoc: true}
				}
				if !arr.IsAssoc {
					arr.IsAssoc = true
					arr.Assoc = make(map[string]string)
					arr.IsIndexed = false
				}
				arr.Assoc[index] = val
				setArray(name, arr)
			} else {
				setArrayElement(name, index, val)
			}
			continue
		}

		if eqIdx >= 0 {
			name := a[:eqIdx]
			val := a[eqIdx+1:]
			if !isValidVarName(name) {
				fmt.Fprintf(os.Stderr, "declare: %s: not a valid identifier\n", name)
				lastExitCode = 1
				continue
			}
			if isAssoc {
				arr := &ArrayVar{Assoc: make(map[string]string), IsAssoc: true}
				parsed := parseAssocLiteral(val)
				for k, v := range parsed {
					arr.Assoc[k] = v
				}
				setArray(name, arr)
			} else if isIndexed {
				elements := parseArrayLiteral(val)
				arr := &ArrayVar{Indexed: elements, IsIndexed: true}
				setArray(name, arr)
			} else {
				setVar(name, val, false)
			}
		} else {
			if !isValidVarName(a) {
				fmt.Fprintf(os.Stderr, "declare: %s: not a valid identifier\n", a)
				lastExitCode = 1
				continue
			}
			if isAssoc {
				arr := getArray(a)
				if arr == nil {
					setArray(a, &ArrayVar{Assoc: make(map[string]string), IsAssoc: true})
				}
			} else if isIndexed {
				arr := getArray(a)
				if arr == nil {
					setArray(a, &ArrayVar{Indexed: []string{}, IsIndexed: true})
				}
			} else {
				if _, ok := varTable[a]; !ok {
					setVar(a, "", false)
				}
			}
		}
	}
	lastExitCode = 0
}

func builtinMapfile(args []string, ctx *ExecContext) {
	stripNewline := false
	maxLines := 0
	origin := 0
	arrayName := "MAPFILE"
	filePath := ""

	i := 1
	for i < len(args) {
		if args[i] == "-t" {
			stripNewline = true
			i++
			continue
		}
		if args[i] == "-n" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "mapfile: -n: option requires an argument")
				lastExitCode = 2
				return
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintf(os.Stderr, "mapfile: %s: invalid number\n", args[i])
				lastExitCode = 2
				return
			}
			maxLines = n
			i++
			continue
		}
		if args[i] == "-O" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "mapfile: -O: option requires an argument")
				lastExitCode = 2
				return
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintf(os.Stderr, "mapfile: %s: invalid number\n", args[i])
				lastExitCode = 2
				return
			}
			origin = n
			i++
			continue
		}
		if len(args[i]) > 0 && args[i][0] == '-' && args[i] != "-" {
			fmt.Fprintf(os.Stderr, "mapfile: %s: invalid option\n", args[i])
			lastExitCode = 2
			return
		}
		break
	}

	remaining := args[i:]
	if len(remaining) >= 1 && remaining[0] != "-" {
		arrayName = remaining[0]
		if len(remaining) >= 2 {
			filePath = remaining[1]
		}
	}

	var reader *bufio.Reader
	if filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mapfile: %s: %s\n", filePath, err)
			lastExitCode = 1
			return
		}
		defer f.Close()
		reader = bufio.NewReader(f)
	} else if ctx.Stdin != os.Stdin {
		reader = bufio.NewReader(ctx.Stdin)
	} else if stdinReader != nil {
		reader = stdinReader
	} else {
		reader = bufio.NewReader(ctx.Stdin)
	}

	var elements []string
	for origin > 0 {
		elements = append(elements, "")
		origin--
	}

	count := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			break
		}
		if err == io.EOF && line == "" {
			break
		}
		if stripNewline {
			line = strings.TrimRight(line, "\r\n")
		} else {
			line = strings.TrimRight(line, "\r")
		}
		elements = append(elements, line)
		count++
		if maxLines > 0 && count >= maxLines {
			break
		}
		if err == io.EOF {
			break
		}
	}

	arr := &ArrayVar{Indexed: elements, IsIndexed: true}
	setArray(arrayName, arr)
	lastExitCode = 0
}

func builtinHash(args []string) {
	if len(args) == 1 {
		names := hashNames()
		if len(names) == 0 {
			lastExitCode = 0
			return
		}
		hashMu.RLock()
		for _, name := range names {
			fmt.Printf("%4d  %s\n", hashHits[name], hashTable[name])
		}
		hashMu.RUnlock()
		lastExitCode = 0
		return
	}

	i := 1
	clearAll := false
	deleteMode := false
	listMode := false
	var manualPath, manualName string

	for i < len(args) && len(args[i]) > 0 && args[i][0] == '-' {
		switch args[i] {
		case "-r":
			clearAll = true
		case "-d":
			deleteMode = true
		case "-l":
			listMode = true
		case "-p":
			if i+2 >= len(args) {
				fmt.Fprintln(os.Stderr, "hash: -p: requires path and name arguments")
				lastExitCode = 2
				return
			}
			manualPath = args[i+1]
			manualName = args[i+2]
			i += 2
		default:
			fmt.Fprintf(os.Stderr, "hash: %s: invalid option\n", args[i])
			lastExitCode = 2
			return
		}
		i++
	}

	if clearAll {
		hashClear()
		lastExitCode = 0
		return
	}

	if manualPath != "" {
		if !hashRegister(manualPath, manualName) {
			fmt.Fprintf(os.Stderr, "hash: %s: not found\n", manualPath)
			lastExitCode = 1
			return
		}
		lastExitCode = 0
		return
	}

	if listMode {
		names := hashNames()
		if len(names) == 0 {
			lastExitCode = 0
			return
		}
		hashMu.RLock()
		for _, name := range names {
			fmt.Printf("builtin hash -p %s %s\n", hashTable[name], name)
		}
		hashMu.RUnlock()
		lastExitCode = 0
		return
	}

	if deleteMode {
		if i >= len(args) {
			fmt.Fprintln(os.Stderr, "hash: -d: requires name arguments")
			lastExitCode = 2
			return
		}
		hashDelete(args[i:])
		lastExitCode = 0
		return
	}

	if i >= len(args) {
		fmt.Fprintln(os.Stderr, "hash: usage: hash [-rdl] [-p path name] [name ...]")
		lastExitCode = 2
		return
	}

	for _, name := range args[i:] {
		resolved, ok := hashLookup(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "hash: %s: not found\n", name)
			lastExitCode = 1
			continue
		}
		hashMu.Lock()
		hashTable[name] = resolved
		if _, exists := hashHits[name]; !exists {
			hashHits[name] = 0
		}
		hashMu.Unlock()
	}
	lastExitCode = 0
}
