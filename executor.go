package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func getExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
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
		if hasExtGlob(t) {
			matches := matchExtGlob(t)
			if len(matches) > 0 {
				result = append(result, matches...)
			} else {
				result = append(result, restoreGlobMarkers(t))
			}
		} else if hasDoubleStar(t) {
			matches := globRecursive(t)
			if len(matches) > 0 {
				result = append(result, matches...)
			} else {
				result = append(result, restoreGlobMarkers(t))
			}
		} else if strings.ContainsAny(t, "*?[") {
			matches, err := customGlob(t)
			if err == nil && len(matches) > 0 {
				result = append(result, matches...)
			} else {
				result = append(result, restoreGlobMarkers(t))
			}
		} else {
			result = append(result, restoreGlobMarkers(t))
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
