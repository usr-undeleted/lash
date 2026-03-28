package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

var lastExitCode int = 0

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
	colorReset  = "\x1b[0m"
	colorRed    = "\x1b[31m"
	colorGreen  = "\x1b[32m"
	colorYellow = "\x1b[33m"
	colorBold   = "\x1b[1m"
	colorCyan   = "\x1b[36m"
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
	user := os.Getenv("USER")
	host, _ := os.Hostname()
	dir, err := os.Getwd()
	if err != nil {
		dir = "?"
	}
	home := os.Getenv("HOME")
	if strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}

	symbol := "$"
	if os.Getuid() == 0 {
		symbol = "#"
	}

	prompt := fmt.Sprintf("%s%s%s@%s%s %sin %s%s%s",
		colorBold, colorCyan, user, host, colorReset,
		colorBold, colorYellow, dir, colorReset)

	branch := getGitBranch()
	if branch != "" {
		prompt += fmt.Sprintf(" %son %s%s%s",
			colorReset, colorBold, branch, colorReset)
	}

	if lastExitCode >= 1 {
		prompt += fmt.Sprintf(" %s✗%s", colorRed, colorReset)
	}

	prompt += fmt.Sprintf("\n%s╰%s%s ", colorBold, symbol, colorReset)

	return prompt
}

func main() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT)
	go func() {
		for range sig {
			fmt.Println()
			fmt.Print(getPrompt())
		}
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(getPrompt())
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		tokens := tokenize(line)
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
				executeBuiltin(segments[0])
				continue
			}
			executeSimple(segments[0], background)
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

	for _, ch := range line {
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' || ch == '\t':
			if !inSingle && !inDouble {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				continue
			}
			current.WriteRune(ch)
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func isBuiltin(cmd string) bool {
	switch cmd {
	case "exit", "cd", "pwd":
		return true
	}
	return false
}

func executeBuiltin(args []string) {
	switch args[0] {
	case "exit":
		os.Exit(lastExitCode)
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

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
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

	if background {
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
			return
		}
		fmt.Printf("[%d]\n", cmd.Process.Pid)
		return
	}

	err := cmd.Run()
	lastExitCode = getExitCode(err)
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
	for i, seg := range segments {
		cmdArgs, inFile, outFile, appendMode := parseRedirection(seg)
		if len(cmdArgs) == 0 {
			return
		}

		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
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
	}

	for _, cmd := range cmds {
		cmd.Start()
	}

	for _, p := range pipes {
		p.w.Close()
	}

	lastExit := 0
	for _, cmd := range cmds {
		err := cmd.Wait()
		lastExit = getExitCode(err)
	}
	lastExitCode = lastExit

	for _, p := range pipes {
		p.r.Close()
	}
}
