package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

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
	inSingle := false
	inDouble := false

	for i < len(s) {
		ch := s[i]

		if inSingle {
			if ch == '\'' {
				inSingle = false
				i++
				continue
			}
			result.WriteByte(ch)
			i++
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = true
			i++
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			i++
			continue
		}

		if ch == '$' && i+1 < len(s) {
			expanded, advanced := expandDollar(s, i, inDouble)
			if advanced > 0 {
				result.WriteString(expanded)
				i += advanced
				continue
			}
		}

		if ch == '`' && !inSingle {
			end, err := findMatchingBacktick(s, i)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				expandError = true
				result.WriteByte(ch)
				i++
				continue
			}
			cmd := s[i+1 : end]
			output, exitCode := runCommandSubstitution(cmd)
			lastExitCode = exitCode
			result.WriteString(output)
			i = end + 1
			continue
		}

		result.WriteByte(ch)
		i++
	}

	return result.String()
}

func expandDollar(s string, pos int, inDouble bool) (string, int) {
	if pos+1 >= len(s) {
		return "", 0
	}

	next := s[pos+1]

	switch {
	case next == '?':
		return strconv.Itoa(lastExitCode), 2

	case next == '$':
		return strconv.Itoa(os.Getpid()), 2

	case next == '{':
		end := strings.Index(s[pos:], "}")
		if end < 0 {
			return "", 0
		}
		inner := s[pos+2 : pos+end]
		if len(inner) > 0 && inner[0] == '#' {
			val := getVar(inner[1:])
			return strconv.Itoa(utf8.RuneCountInString(val)), end + 1
		}
		return getVar(inner), end + 1

	case next == '(':
		if pos+2 < len(s) && s[pos+2] == '(' {
			fmt.Fprintln(os.Stderr, "lash: arithmetic expansion not yet implemented")
			return "", 0
		}
		end, err := findMatchingParen(s, pos+1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			expandError = true
			return "", 0
		}
		cmd := s[pos+2 : end]
		output, exitCode := runCommandSubstitution(cmd)
		lastExitCode = exitCode
		return output, end - pos + 1

	case isAlphaOrUnderscore(next):
		j := pos + 1
		for j < len(s) && isAlnumOrUnderscore(s[j]) {
			j++
		}
		varName := s[pos+1 : j]
		return getVar(varName), j - pos
	}

	return "", 0
}

func findMatchingParen(s string, openPos int) (int, error) {
	depth := 1
	inSingle := false
	inDouble := false
	i := openPos + 1

	for i < len(s) {
		ch := s[i]

		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			i++
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = true
			i++
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			i++
			continue
		}

		if ch == '\\' && inDouble && i+1 < len(s) {
			i += 2
			continue
		}

		if !inSingle && !inDouble {
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
				if depth == 0 {
					return i, nil
				}
			}
		}

		i++
	}

	return -1, fmt.Errorf("unmatched $(")
}

func findMatchingBacktick(s string, openPos int) (int, error) {
	inSingle := false
	inDouble := false
	i := openPos + 1

	for i < len(s) {
		ch := s[i]

		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			i++
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = true
			i++
			continue
		}

		if ch == '"' && !inSingle {
			inDouble = !inDouble
			i++
			continue
		}

		if ch == '\\' && i+1 < len(s) {
			if inDouble {
				if s[i+1] == '"' || s[i+1] == '\\' || s[i+1] == '`' || s[i+1] == '$' {
					i += 2
					continue
				}
			} else {
				i += 2
				continue
			}
		}

		if ch == '`' && !inSingle && !inDouble {
			return i, nil
		}

		i++
	}

	return -1, fmt.Errorf("unmatched `")
}

func runCommandSubstitution(cmd string) (string, int) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", 0
	}

	tokens := tokenize(cmd)
	if len(tokens) == 0 {
		return "", 0
	}

	expanded := expandVariables(tokens)

	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: %s\n", err)
		return "", 1
	}

	oldStdout := os.Stdout
	os.Stdout = w

	chains := splitChains(strings.Join(expanded, " "))
	finalExitCode := 0

	for _, chain := range chains {
		for _, entry := range chain {
			if entry.operator == "&&" && finalExitCode != 0 {
				continue
			}
			if entry.operator == "||" && finalExitCode == 0 {
				continue
			}

			toks := expandVariables(entry.args)
			toks = expandGlobs(toks)
			if len(toks) == 0 {
				continue
			}

			background := false
			if toks[len(toks)-1] == "&" {
				background = true
				toks = toks[:len(toks)-1]
			}

			segments := splitPipes(toks)
			if len(segments) == 0 {
				continue
			}

			if background {
				fmt.Fprintln(os.Stderr, "lash: background jobs not supported in command substitution")
				w.Close()
				os.Stdout = oldStdout
				return "", 1
			}

			if len(segments) == 1 {
				if isBuiltin(segments[0][0]) {
					executeBuiltin(segments[0], nil)
				} else {
					executeSimpleSubstitution(segments[0], w)
				}
			} else {
				executePipelineSubstitution(segments, w)
			}
		}
	}

	os.Stdout = oldStdout
	w.Close()

	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()

	output := strings.TrimRight(buf.String(), "\n")
	return output, lastExitCode
}

func executeSimpleSubstitution(args []string, captureStdout *os.File) {
	cmdArgs, inFile, outFile, appendMode := parseRedirection(args)
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
	} else {
		cmd.Stdin = os.Stdin
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
	} else {
		cmd.Stdout = captureStdout
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

	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(cmd.Process.Pid, &status, 0, nil)
		if err != syscall.EINTR {
			break
		}
	}

	if status.Exited() {
		lastExitCode = status.ExitStatus()
	} else if status.Signaled() {
		lastExitCode = 128 + int(status.Signal())
	}
}

func executePipelineSubstitution(segments [][]string, captureStdout *os.File) {
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
			lastExitCode = 1
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
			cmd.Stdout = captureStdout
		} else {
			cmd.Stdout = pipes[i].w
		}

		cmds = append(cmds, cmd)
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

	var lastStatus syscall.WaitStatus
	for _, cmd := range cmds {
		var status syscall.WaitStatus
		for {
			_, err := syscall.Wait4(cmd.Process.Pid, &status, 0, nil)
			if err != syscall.EINTR {
				break
			}
		}
		lastStatus = status
	}

	if lastStatus.Exited() {
		lastExitCode = lastStatus.ExitStatus()
	} else if lastStatus.Signaled() {
		lastExitCode = 128 + int(lastStatus.Signal())
	}

	for _, p := range pipes {
		p.r.Close()
	}
}

func isAlphaOrUnderscore(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isAlnumOrUnderscore(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}
