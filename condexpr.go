package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

func executeCondExpr(node *CondExpr) {
	if len(node.Tokens) == 0 {
		fmt.Fprintln(os.Stderr, "[[: missing expression")
		lastExitCode = 2
		return
	}

	expanded := expandVariables(node.Tokens)

	p := &dbParser{args: expanded}
	result, code := p.evalOr()
	if code < 0 {
		lastExitCode = 2
		return
	}
	if p.pos != len(p.args) {
		fmt.Fprintf(os.Stderr, "[[: unexpected argument: %s\n", p.args[p.pos])
		lastExitCode = 2
		return
	}
	if result {
		lastExitCode = 0
	} else {
		lastExitCode = 1
	}
}

type dbParser struct {
	args []string
	pos  int
}

func (p *dbParser) peek() string {
	if p.pos >= len(p.args) {
		return ""
	}
	return p.args[p.pos]
}

func (p *dbParser) advance() string {
	t := p.peek()
	if p.pos < len(p.args) {
		p.pos++
	}
	return t
}

func (p *dbParser) evalOr() (bool, int) {
	left, code := p.evalAnd()
	if code < 0 {
		return false, code
	}
	for p.peek() == "||" {
		p.advance()
		right, code := p.evalAnd()
		if code < 0 {
			return false, code
		}
		left = left || right
	}
	return left, 0
}

func (p *dbParser) evalAnd() (bool, int) {
	left, code := p.evalNot()
	if code < 0 {
		return false, code
	}
	for p.peek() == "&&" {
		p.advance()
		right, code := p.evalNot()
		if code < 0 {
			return false, code
		}
		left = left && right
	}
	return left, 0
}

func (p *dbParser) evalNot() (bool, int) {
	if p.peek() == "!" {
		p.advance()
		result, code := p.evalNot()
		if code < 0 {
			return false, code
		}
		return !result, 0
	}
	if p.peek() == "(" {
		p.advance()
		result, code := p.evalOr()
		if code < 0 {
			return false, code
		}
		if p.peek() != ")" {
			fmt.Fprintln(os.Stderr, "[[: missing ')'")
			return false, -1
		}
		p.advance()
		return result, 0
	}
	return p.evalPrimary()
}

func isDBBinaryOp(op string) bool {
	switch op {
	case "==", "!=", "=", "=~", "<", ">",
		"-eq", "-ne", "-lt", "-gt", "-le", "-ge",
		"-nt", "-ot", "-ef":
		return true
	}
	return false
}

func (p *dbParser) evalPrimary() (bool, int) {
	if p.pos >= len(p.args) {
		return false, -1
	}

	t := p.peek()

	if isTestUnaryOp(t) && (p.pos+1 >= len(p.args) || !isDBBinaryOp(p.args[p.pos+1])) {
		op := p.advance()
		if p.pos >= len(p.args) {
			fmt.Fprintf(os.Stderr, "[[: %s: missing argument\n", op)
			return false, -1
		}
		arg := p.advance()
		return evalTestUnary(op, arg)
	}

	if p.pos+1 < len(p.args) && isDBBinaryOp(p.args[p.pos+1]) {
		left := p.advance()
		op := p.advance()
		if p.pos >= len(p.args) {
			fmt.Fprintf(os.Stderr, "[[: %s: missing argument\n", op)
			return false, -1
		}
		right := p.advance()
		return evalDBBinary(left, op, right)
	}

	arg := p.advance()
	return arg != "", 0
}

func evalDBBinary(left, op, right string) (bool, int) {
	switch op {
	case "==", "=":
		matched, err := filepath.Match(right, left)
		if err != nil {
			return left == right, 0
		}
		return matched, 0
	case "!=":
		matched, err := filepath.Match(right, left)
		if err != nil {
			return left != right, 0
		}
		return !matched, 0
	case "=~":
		re, err := regexp.Compile(right)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[[: %s: invalid regex\n", right)
			return false, -1
		}
		return re.MatchString(left), 0
	case "<":
		return left < right, 0
	case ">":
		return left > right, 0
	case "-eq", "-ne", "-lt", "-gt", "-le", "-ge":
		a, err1 := strconv.ParseInt(left, 10, 64)
		b, err2 := strconv.ParseInt(right, 10, 64)
		if err1 != nil || err2 != nil {
			bad := left
			if err1 == nil {
				bad = right
			}
			fmt.Fprintf(os.Stderr, "[[: %s: integer expression expected\n", bad)
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
