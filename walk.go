package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type ExecContext struct {
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
	Cfg    *Config
}

func defaultContext() *ExecContext {
	return &ExecContext{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Cfg:    currentConfig,
	}
}

func (ctx *ExecContext) withOverrides(stdin, stdout, stderr *os.File) *ExecContext {
	c := *ctx
	if stdin != nil {
		c.Stdin = stdin
	}
	if stdout != nil {
		c.Stdout = stdout
	}
	if stderr != nil {
		c.Stderr = stderr
	}
	return &c
}

func executeNode(node Node, ctx *ExecContext) {
	if node == nil {
		return
	}
	if returnFlag {
		return
	}
	switch n := node.(type) {
	case *Program:
		executeProgram(n, ctx)
	case *AndOr:
		executeAndOr(n, ctx)
	case *Pipeline:
		executePipelineNode(n, ctx)
	case *Command:
		executeCommandNode(n, ctx)
	case *IfStmt:
		executeIf(n, ctx)
	case *WhileStmt:
		executeWhile(n, ctx)
	case *ForStmt:
		executeFor(n, ctx)
	case *CStyleForStmt:
		executeCStyleFor(n, ctx)
	case *SelectStmt:
		executeSelect(n, ctx)
	case *FuncDef:
		defineFunc(n.Name, n)
	case *CaseStmt:
		executeCase(n, ctx)
	case *BreakStmt:
		breakFlag = true
	case *ContinueStmt:
		continueFlag = true
	}
}

func executeProgram(prog *Program, ctx *ExecContext) {
	for _, cmd := range prog.Commands {
		if returnFlag || breakFlag || continueFlag {
			return
		}
		executeNode(cmd, ctx)
	}
}

func executeAndOr(node *AndOr, ctx *ExecContext) {
	executeNode(node.Left, ctx)
	if node.Op == "&&" && lastExitCode != 0 {
		return
	}
	if node.Op == "||" && lastExitCode == 0 {
		return
	}
	executeNode(node.Right, ctx)
}

func executeCommandNode(cmd *Command, ctx *ExecContext) {
	if len(cmd.Assignments) > 0 && len(cmd.Args) == 0 {
		for _, a := range cmd.Assignments {
			val := expandString(a.Value)
			setVar(a.Name, val, false)
		}
		lastExitCode = 0
		return
	}

	expanded := expandBraces(cmd.Args)
	expanded = expandVariables(expanded)
	if expandError {
		expandError = false
		lastExitCode = 2
		waitProcSubst()
		return
	}
	expanded = expandGlobs(expanded)
	if len(expanded) == 0 {
		return
	}

	prefixEnv := make(map[string]string)
	if len(cmd.Assignments) > 0 {
		for _, a := range cmd.Assignments {
			val := expandString(a.Value)
			prefixEnv[a.Name] = val
		}
	}

	redirCtx, cleanup, redirErr := applyRedirections(cmd.Redirections, ctx)
	if redirErr {
		cleanup()
		return
	}
	defer cleanup()

	if fn := lookupFunc(expanded[0]); fn != nil {
		pushScope()
		savedParams := positionalParams
		positionalParams = expanded[1:]
		setVar("0", expanded[0], false)
		returnFlag = false
		executeNode(fn.Body, ctx)
		returnFlag = false
		popScope()
		positionalParams = savedParams
		waitProcSubst()
		return
	}

	if isBuiltin(expanded[0]) {
		if len(prefixEnv) > 0 {
			for name, val := range prefixEnv {
				setVar(name, val, false)
			}
			executeBuiltin(expanded, ctx.Cfg)
			for name := range prefixEnv {
				unsetVar(name)
			}
		} else {
			executeBuiltin(expanded, ctx.Cfg)
		}
		waitProcSubst()
		return
	}

	if cmd.Background {
		executeBackground(expanded, redirCtx, prefixEnv)
	} else {
		executeForeground(expanded, redirCtx, prefixEnv)
	}
	waitProcSubst()
}

func executePipelineNode(pipe *Pipeline, ctx *ExecContext) {
	if len(pipe.Commands) == 1 {
		executeNode(pipe.Commands[0], ctx)
		if pipe.Negated {
			if lastExitCode == 0 {
				lastExitCode = 1
			} else {
				lastExitCode = 0
			}
		}
		return
	}

	type pipePair struct {
		r *os.File
		w *os.File
	}

	pipes := make([]pipePair, len(pipe.Commands)-1)
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
	for i, node := range pipe.Commands {
		cmdNode, ok := node.(*Command)
		if !ok {
			continue
		}

		expanded := expandBraces(cmdNode.Args)
		expanded = expandVariables(expanded)
		if expandError {
			expandError = false
			lastExitCode = 2
			for _, p := range pipes {
				p.r.Close()
				p.w.Close()
			}
			waitProcSubst()
			return
		}
		expanded = expandGlobs(expanded)
		if len(expanded) == 0 {
			for _, p := range pipes {
				p.r.Close()
				p.w.Close()
			}
			return
		}

		resolvedArgs, extraFiles := resolveProcSubstArgs(expanded)

		var env []string
		if len(cmdNode.Assignments) > 0 {
			prefixEnv := make(map[string]string)
			for _, a := range cmdNode.Assignments {
				val := expandString(a.Value)
				prefixEnv[a.Name] = val
			}
			env = buildEnvWithPrefix(prefixEnv)
		}

		c := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		c.Stderr = ctx.Stderr
		if len(extraFiles) > 0 {
			c.ExtraFiles = extraFiles
		}
		if env != nil {
			c.Env = env
		}

		stdin := ctx.Stdin
		if i > 0 {
			stdin = pipes[i-1].r
		}

		stdout := ctx.Stdout
		if i < len(pipe.Commands)-1 {
			stdout = pipes[i].w
		}

		c.Stdin = stdin
		c.Stdout = stdout

		for _, redir := range cmdNode.Redirections {
			switch redir.Op {
			case "<":
				if f := resolveProcSubstFile(redir.Target); f != nil {
					c.Stdin = f
				} else {
					f, err := os.Open(redir.Target)
					if err != nil {
						fmt.Fprintf(os.Stderr, "lash: %s\n", err)
						lastExitCode = 1
						for _, p := range pipes {
							p.r.Close()
							p.w.Close()
						}
						return
					}
					defer f.Close()
					c.Stdin = f
				}
			case ">":
				if f := resolveProcSubstFile(redir.Target); f != nil {
					c.Stdout = f
				} else {
					flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
					f, err := os.OpenFile(redir.Target, flag, 0644)
					if err != nil {
						fmt.Fprintf(os.Stderr, "lash: %s\n", err)
						lastExitCode = 1
						for _, p := range pipes {
							p.r.Close()
							p.w.Close()
						}
						return
					}
					defer f.Close()
					c.Stdout = f
				}
			case ">>":
				if f := resolveProcSubstFile(redir.Target); f != nil {
					c.Stdout = f
				} else {
					flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
					f, err := os.OpenFile(redir.Target, flag, 0644)
					if err != nil {
						fmt.Fprintf(os.Stderr, "lash: %s\n", err)
						lastExitCode = 1
						for _, p := range pipes {
							p.r.Close()
							p.w.Close()
						}
						return
					}
					defer f.Close()
					c.Stdout = f
				}
			}
		}

		cmds = append(cmds, c)
	}

	for _, c := range cmds {
		if err := c.Start(); err != nil {
			if _, ok := err.(*exec.Error); ok {
				fmt.Fprintf(os.Stderr, "lash: %s: command not found\n", c.Path)
				lastExitCode = 127
			} else {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
			}
			for _, p := range pipes {
				p.r.Close()
				p.w.Close()
			}
			return
		}
	}

	for _, p := range pipes {
		p.w.Close()
	}

	pids := make([]int, len(cmds))
	for i, c := range cmds {
		pids[i] = c.Process.Pid
	}
	pgid := pids[0]

	var cmdArgsList [][]string
	for _, c := range cmds {
		cmdArgsList = append(cmdArgsList, c.Args)
	}
	commandStr := strings.Join(cmdArgsList[0], " ")
	for _, args := range cmdArgsList[1:] {
		commandStr += " | " + strings.Join(args, " ")
	}

	if pipe.Commands[0].(*Command).Background {
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

	if pipe.Negated {
		if lastExitCode == 0 {
			lastExitCode = 1
		} else {
			lastExitCode = 0
		}
	}
}

func executeIf(node *IfStmt, ctx *ExecContext) {
	executeNode(node.Condition, ctx)
	if lastExitCode == 0 {
		executeNode(node.Body, ctx)
		return
	}
	for _, elif := range node.Elifs {
		if returnFlag {
			return
		}
		executeNode(elif.Condition, ctx)
		if lastExitCode == 0 {
			executeNode(elif.Body, ctx)
			return
		}
	}
	executeNode(node.Else, ctx)
}

func executeWhile(node *WhileStmt, ctx *ExecContext) {
	for {
		if returnFlag {
			return
		}
		breakFlag = false
		continueFlag = false
		executeNode(node.Condition, ctx)
		if returnFlag {
			return
		}
		shouldRun := lastExitCode == 0
		if node.Until {
			shouldRun = lastExitCode != 0
		}
		if !shouldRun {
			break
		}
		executeNode(node.Body, ctx)
		if returnFlag {
			return
		}
		if breakFlag {
			breakFlag = false
			return
		}
		if continueFlag {
			continue
		}
	}
}

func executeFor(node *ForStmt, ctx *ExecContext) {
	words := expandBraces(node.Words)
	words = expandVariables(words)
	words = expandGlobs(words)

	for _, val := range words {
		if returnFlag {
			return
		}
		breakFlag = false
		continueFlag = false
		setVar(node.Var, val, false)
		executeNode(node.Body, ctx)
		if returnFlag {
			return
		}
		if breakFlag {
			breakFlag = false
			return
		}
		if continueFlag {
			continue
		}
	}
}

func executeCStyleFor(node *CStyleForStmt, ctx *ExecContext) {
	if node.Init != "" {
		evalArithmetic(node.Init)
	}
	for {
		if returnFlag {
			return
		}
		breakFlag = false
		continueFlag = false
		if node.Cond != "" {
			result := evalArithmetic(node.Cond)
			if result == "0" {
				break
			}
		}
		executeNode(node.Body, ctx)
		if returnFlag {
			return
		}
		if breakFlag {
			breakFlag = false
			return
		}
		if continueFlag {
			if node.Step != "" {
				evalArithmetic(node.Step)
			}
			continue
		}
		if node.Step != "" {
			evalArithmetic(node.Step)
		}
	}
}

func executeSelect(node *SelectStmt, ctx *ExecContext) {
	words := expandBraces(node.Words)
	words = expandVariables(words)
	words = expandGlobs(words)

	var reader *bufio.Reader
	if stdinReader != nil {
		reader = stdinReader
	} else {
		reader = bufio.NewReader(ctx.Stdin)
	}

	prompt := getVar("PS3")
	if prompt == "" {
		prompt = "#? "
	}

	for {
		if returnFlag {
			return
		}
		breakFlag = false
		continueFlag = false

		for i, w := range words {
			fmt.Fprintf(ctx.Stderr, "%d) %s\n", i+1, w)
		}
		fmt.Fprintf(ctx.Stderr, "%s", prompt)

		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\n\r")

		setVar("REPLY", line, false)

		num, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || num < 1 || num > len(words) {
			setVar(node.Var, "", false)
		} else {
			setVar(node.Var, words[num-1], false)
		}

		executeNode(node.Body, ctx)
		if returnFlag {
			return
		}
		if breakFlag {
			breakFlag = false
			return
		}
		if continueFlag {
			continue
		}
	}
}

func executeCase(node *CaseStmt, ctx *ExecContext) {
	word := expandString(node.Word)
	for _, branch := range node.Branches {
		matched := false
		for _, pat := range branch.Patterns {
			expandedPat := expandString(pat)
			if match, _ := filepath.Match(expandedPat, word); match {
				matched = true
				break
			}
			if strings.Contains(expandedPat, "*") || strings.Contains(expandedPat, "?") || strings.Contains(expandedPat, "[") {
				continue
			}
			if expandedPat == word {
				matched = true
				break
			}
		}
		if matched {
			executeNode(branch.Body, ctx)
			return
		}
	}
}

func applyRedirections(redirs []Redir, ctx *ExecContext) (*ExecContext, func(), bool) {
	if len(redirs) == 0 {
		return ctx, func() {}, false
	}

	var opened []*os.File
	newCtx := ctx

	for _, r := range redirs {
		switch r.Op {
		case "<":
			if f := resolveProcSubstFile(r.Target); f != nil {
				newCtx = newCtx.withOverrides(f, nil, nil)
			} else {
				f, err := os.Open(r.Target)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					lastExitCode = 1
					return ctx, func() {
						for _, of := range opened {
							of.Close()
						}
					}, true
				}
				opened = append(opened, f)
				newCtx = newCtx.withOverrides(f, nil, nil)
			}
		case ">":
			if f := resolveProcSubstFile(r.Target); f != nil {
				newCtx = newCtx.withOverrides(nil, f, nil)
			} else {
				flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
				f, err := os.OpenFile(r.Target, flag, 0644)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					lastExitCode = 1
					return ctx, func() {
						for _, of := range opened {
							of.Close()
						}
					}, true
				}
				opened = append(opened, f)
				newCtx = newCtx.withOverrides(nil, f, nil)
			}
		case ">>":
			if f := resolveProcSubstFile(r.Target); f != nil {
				newCtx = newCtx.withOverrides(nil, f, nil)
			} else {
				flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
				f, err := os.OpenFile(r.Target, flag, 0644)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					lastExitCode = 1
					return ctx, func() {
						for _, of := range opened {
							of.Close()
						}
					}, true
				}
				opened = append(opened, f)
				newCtx = newCtx.withOverrides(nil, f, nil)
			}
		}
	}

	return newCtx, func() {
		for _, f := range opened {
			f.Close()
		}
	}, false
}

func executeForeground(args []string, ctx *ExecContext, prefixEnv map[string]string) {
	resolvedArgs, extraFiles := resolveProcSubstArgs(args)

	cmd := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = ctx.Stdin
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = extraFiles
	}
	if len(prefixEnv) > 0 {
		cmd.Env = buildEnvWithPrefix(prefixEnv)
	}

	err := cmd.Start()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			fmt.Fprintf(ctx.Stderr, "lash: %s: command not found\n", resolvedArgs[0])
			lastExitCode = 127
		} else {
			fmt.Fprintf(ctx.Stderr, "lash: %s\n", err)
			lastExitCode = 1
		}
		return
	}

	commandStr := strings.Join(resolvedArgs, " ")
	exitCode := waitForeground([]int{cmd.Process.Pid}, cmd.Process.Pid, commandStr)
	if exitCode < 0 {
		lastExitCode = 128 + int(syscall.SIGTSTP)
	} else {
		lastExitCode = exitCode
	}
}

func executeBackground(args []string, ctx *ExecContext, prefixEnv map[string]string) {
	resolvedArgs, extraFiles := resolveProcSubstArgs(args)

	cmd := exec.Command(resolvedArgs[0], resolvedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = ctx.Stdin
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = extraFiles
	}
	if len(prefixEnv) > 0 {
		cmd.Env = buildEnvWithPrefix(prefixEnv)
	}

	err := cmd.Start()
	if err != nil {
		if _, ok := err.(*exec.Error); ok {
			fmt.Fprintf(ctx.Stderr, "lash: %s: command not found\n", resolvedArgs[0])
			lastExitCode = 127
		} else {
			fmt.Fprintf(ctx.Stderr, "lash: %s\n", err)
			lastExitCode = 1
		}
		return
	}

	pid := cmd.Process.Pid
	commandStr := strings.Join(resolvedArgs, " ")
	job := addJob(pid, pid, JobRunning, commandStr)
	fmt.Printf("[%d] %d\n", job.Number, pid)
	lastExitCode = 0
}
