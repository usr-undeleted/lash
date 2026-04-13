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
	"sync/atomic"
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
	case *Subshell:
		executeSubshell(n, ctx)
	case *Group:
		executeNode(n.Body, ctx)
	case *CondExpr:
		executeCondExpr(n)
	case *ArrayAssign:
		executeArrayAssign(n)
	}
}

func checkInterrupt() bool {
	return atomic.LoadInt32(&interruptFlag) != 0
}

func clearInterrupt() int {
	sig := atomic.LoadInt32(&interruptSignal)
	atomic.StoreInt32(&interruptFlag, 0)
	return int(sig)
}

func signalInterruptFromExitCode(code int) {
	if code < 0 {
		atomic.StoreInt32(&interruptFlag, 1)
		atomic.StoreInt32(&interruptSignal, int32(syscall.SIGTSTP))
	} else if code == 128+int(syscall.SIGINT) {
		atomic.StoreInt32(&interruptFlag, 1)
		atomic.StoreInt32(&interruptSignal, int32(syscall.SIGINT))
	} else if code == 128+int(syscall.SIGTSTP) {
		atomic.StoreInt32(&interruptFlag, 1)
		atomic.StoreInt32(&interruptSignal, int32(syscall.SIGTSTP))
	}
}

func executeProgram(prog *Program, ctx *ExecContext) {
	for _, cmd := range prog.Commands {
		if returnFlag || breakFlag || continueFlag || checkInterrupt() {
			return
		}
		executeNode(cmd, ctx)
		if checkInterrupt() {
			lastExitCode = 128 + clearInterrupt()
			return
		}
		if setErrExit && !inCondition && lastExitCode != 0 {
			os.Exit(lastExitCode)
		}
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
			bracketIdx := strings.Index(a.Name, "[")
			if bracketIdx >= 0 && strings.HasSuffix(a.Name, "]") {
				arrName := a.Name[:bracketIdx]
				index := a.Name[bracketIdx+1 : len(a.Name)-1]
				setArrayElement(arrName, index, val)
			} else {
				setVar(a.Name, val, false)
			}
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

	if setXTrace {
		fmt.Fprintf(ctx.Stderr, "+ %s\n", strings.Join(expanded, " "))
	}

	prefixEnv := make(map[string]string)
	var prefixArrAssigns []Assignment
	if len(cmd.Assignments) > 0 {
		for _, a := range cmd.Assignments {
			bracketIdx := strings.Index(a.Name, "[")
			if bracketIdx >= 0 && strings.HasSuffix(a.Name, "]") {
				prefixArrAssigns = append(prefixArrAssigns, a)
			} else {
				val := expandString(a.Value)
				prefixEnv[a.Name] = val
			}
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
		pushArrayScope()
		savedParams := positionalParams
		positionalParams = expanded[1:]
		setVar("0", expanded[0], false)
		returnFlag = false
		executeNode(fn.Body, ctx)
		returnFlag = false
		popArrayScope()
		popScope()
		positionalParams = savedParams
		waitProcSubst()
		return
	}

	if isBuiltin(expanded[0]) {
		if len(prefixEnv) > 0 || len(prefixArrAssigns) > 0 {
			for name, val := range prefixEnv {
				setVar(name, val, false)
			}
			for _, a := range prefixArrAssigns {
				val := expandString(a.Value)
				bracketIdx := strings.Index(a.Name, "[")
				arrName := a.Name[:bracketIdx]
				index := a.Name[bracketIdx+1 : len(a.Name)-1]
				setArrayElement(arrName, index, val)
			}
			executeBuiltin(expanded, ctx)
			for name := range prefixEnv {
				unsetVar(name)
			}
			for _, a := range prefixArrAssigns {
				bracketIdx := strings.Index(a.Name, "[")
				arrName := a.Name[:bracketIdx]
				unsetArray(arrName)
			}
		} else {
			executeBuiltin(expanded, ctx)
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

	codes := waitForegroundCodes(pids, pgid, commandStr)
	var exitCode int
	if len(codes) == 0 {
		exitCode = 0
	} else if setPipefail {
		exitCode = 0
		for _, c := range codes {
			if c != 0 {
				exitCode = c
			}
		}
	} else {
		exitCode = codes[len(codes)-1]
	}
	if exitCode < 0 {
		lastExitCode = 128 + int(syscall.SIGTSTP)
	} else {
		lastExitCode = exitCode
	}
	signalInterruptFromExitCode(lastExitCode)

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
	inCondition = true
	executeNode(node.Condition, ctx)
	inCondition = false
	if lastExitCode == 0 {
		executeNode(node.Body, ctx)
		return
	}
	for _, elif := range node.Elifs {
		if returnFlag {
			return
		}
		inCondition = true
		executeNode(elif.Condition, ctx)
		inCondition = false
		if lastExitCode == 0 {
			executeNode(elif.Body, ctx)
			return
		}
	}
	executed := false
	if node.Else != nil {
		executeNode(node.Else, ctx)
		executed = true
	}
	if !executed {
		lastExitCode = 0
	}
}

func executeWhile(node *WhileStmt, ctx *ExecContext) {
	for {
		if returnFlag || checkInterrupt() {
			return
		}
		breakFlag = false
		continueFlag = false
		inCondition = true
		executeNode(node.Condition, ctx)
		inCondition = false
		if checkInterrupt() {
			lastExitCode = 128 + clearInterrupt()
			return
		}
		if returnFlag {
			return
		}
		shouldRun := lastExitCode == 0
		if node.Until {
			shouldRun = lastExitCode != 0
		}
		if !shouldRun {
			lastExitCode = 0
			break
		}
		executeNode(node.Body, ctx)
		if checkInterrupt() {
			lastExitCode = 128 + clearInterrupt()
			return
		}
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
		if returnFlag || checkInterrupt() {
			return
		}
		breakFlag = false
		continueFlag = false
		setVar(node.Var, val, false)
		executeNode(node.Body, ctx)
		if checkInterrupt() {
			lastExitCode = 128 + clearInterrupt()
			return
		}
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
		if returnFlag || checkInterrupt() {
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
		if checkInterrupt() {
			lastExitCode = 128 + clearInterrupt()
			return
		}
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
		if returnFlag || checkInterrupt() {
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
		if checkInterrupt() {
			lastExitCode = 128 + clearInterrupt()
			return
		}
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

func executeArrayAssign(node *ArrayAssign) {
	expanded := make([]string, len(node.Elements))
	for i, elem := range node.Elements {
		expanded[i] = expandString(elem)
	}

	assocMode := false
	for _, elem := range expanded {
		if strings.HasPrefix(elem, "[") {
			assocMode = true
			break
		}
	}

	if assocMode || node.IsAssoc {
		arr := &ArrayVar{Assoc: make(map[string]string), IsAssoc: true}
		for _, elem := range expanded {
			if strings.HasPrefix(elem, "[") {
				closeBracket := strings.Index(elem[1:], "]")
				if closeBracket >= 0 {
					key := elem[1 : closeBracket+1]
					var val string
					if closeBracket+2 < len(elem) && elem[closeBracket+2] == '=' {
						val = elem[closeBracket+3:]
					}
					arr.Assoc[key] = val
				}
			} else {
				eqIdx := strings.Index(elem, "=")
				if eqIdx >= 0 {
					arr.Assoc[elem[:eqIdx]] = elem[eqIdx+1:]
				}
			}
		}
		setArray(node.Name, arr)
	} else {
		arr := &ArrayVar{Indexed: expanded, IsIndexed: true}
		setArray(node.Name, arr)
	}
	lastExitCode = 0
}

func applyRedirections(redirs []Redir, ctx *ExecContext) (*ExecContext, func(), bool) {
	if len(redirs) == 0 {
		return ctx, func() {}, false
	}

	var opened []*os.File
	newCtx := ctx

	for _, r := range redirs {
		switch r.Op {
		case "<<", "<<-":
			info, ok := heredocMap[r.Target]
			if !ok {
				fmt.Fprintf(os.Stderr, "lash: heredoc: content not found\n")
				lastExitCode = 1
				return ctx, func() {
					for _, of := range opened {
						of.Close()
					}
				}, true
			}
			content := info.Content
			if !info.Quoted {
				content = expandString(content)
			}
			pr, pw, err := os.Pipe()
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
				return ctx, func() {
					for _, of := range opened {
						of.Close()
					}
				}, true
			}
			go func() {
				pw.WriteString(content)
				pw.Close()
			}()
			opened = append(opened, pr)
			newCtx = newCtx.withOverrides(pr, nil, nil)
		case "<<<":
			expanded := expandString(stripQuotes(r.Target))
			pr, pw, err := os.Pipe()
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				lastExitCode = 1
				return ctx, func() {
					for _, of := range opened {
						of.Close()
					}
				}, true
			}
			go func() {
				pw.WriteString(expanded)
				pw.Write([]byte{'\n'})
				pw.Close()
			}()
			opened = append(opened, pr)
			newCtx = newCtx.withOverrides(pr, nil, nil)
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
	signalInterruptFromExitCode(lastExitCode)
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

func executeSubshell(node *Subshell, ctx *ExecContext) {
	savedVars := snapshotVars()
	savedArrs := snapshotArrays()
	savedDir, _ := os.Getwd()
	savedParams := make([]string, len(positionalParams))
	copy(savedParams, positionalParams)

	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: %s\n", err)
		lastExitCode = 1
		return
	}

	savedStdout := os.Stdout
	os.Stdout = w

	oldInSubshell := inSubshell
	inSubshell = true

	executeNode(node.Body, ctx)

	os.Stdout = savedStdout
	w.Close()
	io.Copy(savedStdout, r)
	r.Close()

	inSubshell = oldInSubshell
	restoreVars(savedVars)
	restoreArrays(savedArrs)
	positionalParams = savedParams
	os.Chdir(savedDir)
}

func snapshotVars() map[string]string {
	varMu.Lock()
	defer varMu.Unlock()
	s := make(map[string]string, len(varTable))
	for k, v := range varTable {
		s[k] = v
	}
	return s
}

func restoreVars(saved map[string]string) {
	varMu.Lock()
	defer varMu.Unlock()
	varTable = make(map[string]string, len(saved))
	for k, v := range saved {
		varTable[k] = v
	}
}
