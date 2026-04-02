package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

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
