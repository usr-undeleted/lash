package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/term"
)

func builtinTest(args []string) {
	cmd := args[0]
	var exprArgs []string
	if cmd == "[" {
		if len(args) < 2 || args[len(args)-1] != "]" {
			fmt.Fprintln(os.Stderr, "[: missing ']'")
			lastExitCode = 2
			return
		}
		exprArgs = args[1 : len(args)-1]
	} else {
		exprArgs = args[1:]
	}

	if len(exprArgs) == 0 {
		lastExitCode = 1
		return
	}

	tp := &testParser{args: exprArgs, cmd: cmd}
	result, code := tp.evalOr()
	if code < 0 {
		lastExitCode = 2
		return
	}
	if tp.pos != len(tp.args) {
		fmt.Fprintf(os.Stderr, "%s: unexpected argument: %s\n", cmd, tp.args[tp.pos])
		lastExitCode = 2
		return
	}
	if result {
		lastExitCode = 0
	} else {
		lastExitCode = 1
	}
}

type testParser struct {
	args []string
	pos  int
	cmd  string
}

func (tp *testParser) peek() string {
	if tp.pos >= len(tp.args) {
		return ""
	}
	return tp.args[tp.pos]
}

func (tp *testParser) advance() string {
	t := tp.peek()
	if tp.pos < len(tp.args) {
		tp.pos++
	}
	return t
}

func (tp *testParser) unescaped() string {
	t := tp.peek()
	if len(t) == 2 && t[0] == '\\' {
		return string(t[1])
	}
	return t
}

func (tp *testParser) evalOr() (bool, int) {
	left, code := tp.evalAnd()
	if code < 0 {
		return false, code
	}
	for tp.peek() == "-o" {
		tp.advance()
		right, code := tp.evalAnd()
		if code < 0 {
			return false, code
		}
		left = left || right
	}
	return left, 0
}

func (tp *testParser) evalAnd() (bool, int) {
	left, code := tp.evalNot()
	if code < 0 {
		return false, code
	}
	for tp.peek() == "-a" {
		tp.advance()
		right, code := tp.evalNot()
		if code < 0 {
			return false, code
		}
		left = left && right
	}
	return left, 0
}

func (tp *testParser) evalNot() (bool, int) {
	if tp.peek() == "!" {
		tp.advance()
		result, code := tp.evalNot()
		if code < 0 {
			return false, code
		}
		return !result, 0
	}
	if tp.unescaped() == "(" {
		tp.advance()
		result, code := tp.evalOr()
		if code < 0 {
			return false, code
		}
		if tp.unescaped() != ")" {
			fmt.Fprintf(os.Stderr, "%s: missing ')'\n", tp.cmd)
			return false, -1
		}
		tp.advance()
		return result, 0
	}
	return tp.evalPrimary()
}

func isTestUnaryOp(op string) bool {
	switch op {
	case "-e", "-f", "-d", "-r", "-w", "-x", "-s",
		"-L", "-h", "-b", "-c", "-p", "-S",
		"-n", "-z", "-t", "-u", "-g", "-k", "-O", "-G", "-N":
		return true
	}
	return false
}

func isTestBinaryOp(op string) bool {
	switch op {
	case "=", "!=", "-eq", "-ne", "-lt", "-gt", "-le", "-ge",
		"-nt", "-ot", "-ef":
		return true
	}
	return false
}

func (tp *testParser) evalPrimary() (bool, int) {
	if tp.pos >= len(tp.args) {
		return false, -1
	}

	t := tp.peek()

	if isTestUnaryOp(t) && tp.pos+1 < len(tp.args) {
		op := tp.advance()
		arg := tp.advance()
		return evalTestUnary(op, arg)
	}

	if tp.pos+1 < len(tp.args) && isTestBinaryOp(tp.args[tp.pos+1]) {
		left := tp.advance()
		op := tp.advance()
		if tp.pos >= len(tp.args) {
			fmt.Fprintf(os.Stderr, "%s: %s: missing argument\n", tp.cmd, op)
			return false, -1
		}
		right := tp.advance()
		return evalTestBinary(tp.cmd, left, op, right)
	}

	arg := tp.advance()
	return arg != "", 0
}

func evalTestUnary(op, arg string) (bool, int) {
	switch op {
	case "-e":
		_, err := os.Stat(arg)
		return err == nil, 0
	case "-f":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode().IsRegular(), 0
	case "-d":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.IsDir(), 0
	case "-r":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return testFilePerm(info, 4), 0
	case "-w":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return testFilePerm(info, 2), 0
	case "-x":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return testFilePerm(info, 1), 0
	case "-s":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Size() > 0, 0
	case "-L", "-h":
		info, err := os.Lstat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeSymlink != 0, 0
	case "-b":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0, 0
	case "-c":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeCharDevice != 0, 0
	case "-p":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeNamedPipe != 0, 0
	case "-S":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeSocket != 0, 0
	case "-n":
		return arg != "", 0
	case "-z":
		return arg == "", 0
	case "-t":
		fd := 1
		if arg != "" {
			n, err := strconv.Atoi(arg)
			if err != nil {
				return false, 1
			}
			fd = n
		}
		return term.IsTerminal(fd), 0
	case "-u":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeSetuid != 0, 0
	case "-g":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeSetgid != 0, 0
	case "-k":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		return info.Mode()&os.ModeSticky != 0, 0
	case "-O":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return false, 0
		}
		return int(st.Uid) == os.Getuid(), 0
	case "-G":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return false, 0
		}
		return int(st.Gid) == os.Getgid(), 0
	case "-N":
		info, err := os.Stat(arg)
		if err != nil {
			return false, 0
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return false, 0
		}
		if st.Mtim.Sec != st.Atim.Sec {
			return st.Mtim.Sec > st.Atim.Sec, 0
		}
		return st.Mtim.Nsec > st.Atim.Nsec, 0
	}
	return false, -1
}

func evalTestBinary(cmd, left, op, right string) (bool, int) {
	switch op {
	case "=":
		return left == right, 0
	case "!=":
		return left != right, 0
	case "-eq", "-ne", "-lt", "-gt", "-le", "-ge":
		a, err1 := strconv.ParseInt(left, 10, 64)
		b, err2 := strconv.ParseInt(right, 10, 64)
		if err1 != nil || err2 != nil {
			bad := left
			if err1 == nil {
				bad = right
			}
			fmt.Fprintf(os.Stderr, "%s: %s: integer expression expected\n", cmd, bad)
			return false, -1
		}
		switch op {
		case "-eq":
			return a == b, 0
		case "-ne":
			return a != b, 0
		case "-lt":
			return a < b, 0
		case "-gt":
			return a > b, 0
		case "-le":
			return a <= b, 0
		case "-ge":
			return a >= b, 0
		}
	case "-nt":
		info1, err1 := os.Stat(left)
		info2, err2 := os.Stat(right)
		if err1 != nil {
			return false, 0
		}
		if err2 != nil {
			return true, 0
		}
		return info1.ModTime().After(info2.ModTime()), 0
	case "-ot":
		info1, err1 := os.Stat(left)
		info2, err2 := os.Stat(right)
		if err1 != nil {
			return true, 0
		}
		if err2 != nil {
			return false, 0
		}
		return info1.ModTime().Before(info2.ModTime()), 0
	case "-ef":
		info1, err1 := os.Stat(left)
		info2, err2 := os.Stat(right)
		if err1 != nil || err2 != nil {
			return false, 0
		}
		return os.SameFile(info1, info2), 0
	}
	return false, -1
}

func testFilePerm(info os.FileInfo, check uint32) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	uid := os.Getuid()
	gid := os.Getgid()
	var perm uint32
	if uid == int(st.Uid) {
		perm = (st.Mode >> 6) & 7
	} else if gid == int(st.Gid) {
		perm = (st.Mode >> 3) & 7
	} else {
		perm = st.Mode & 7
	}
	return perm&check != 0
}
