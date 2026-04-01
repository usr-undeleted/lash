package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
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
		} else {
			end := 1
			for end < len(s) && s[end] != '/' {
				end++
			}
			username := s[1:end]
			if u, err := user.Lookup(username); err == nil && u.HomeDir != "" {
				s = u.HomeDir + s[end:]
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

		if !inDouble && (ch == '<' || ch == '>') && i+1 < len(s) && s[i+1] == '(' {
			end := findMatchingProcSubstParen(s, i+2)
			if end != -1 {
				token := s[i : end+1]
				replaced := expandProcSubst(token)
				if replaced != "" || expandError {
					result.WriteString(replaced)
					i = end + 1
					continue
				}
			}
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
		end, err := findMatchingBrace(s, pos+1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			expandError = true
			return "", 0
		}
		inner := s[pos+2 : end]

		// ${#VAR} — length expansion
		if len(inner) > 0 && inner[0] == '#' {
			val := getVar(inner[1:])
			return strconv.Itoa(utf8.RuneCountInString(val)), end - pos + 1
		}

		// ${!ref} — variable indirection
		if len(inner) > 0 && inner[0] == '!' {
			refName := inner[1:]
			target := getVar(refName)
			return getVar(target), end - pos + 1
		}

		// ${VAR:-default}, ${VAR:=default}, ${VAR:+alt}, ${VAR:?err}
		varName, operand, op := parseBraceExpansion(inner)
		if op != "" {
			val := getVar(varName)
			switch op {
			case ":-":
				if val == "" {
					return expandString(operand), end - pos + 1
				}
				return val, end - pos + 1
			case ":=":
				if val == "" {
					expanded := expandString(operand)
					// Preserve exported status if variable was previously exported
					_, isExported := exportedVars[varName]
					setVar(varName, expanded, isExported)
					return expanded, end - pos + 1
				}
				return val, end - pos + 1
			case ":+":
				if val != "" {
					return expandString(operand), end - pos + 1
				}
				return "", end - pos + 1
			case ":?":
				if val == "" {
					errMsg := operand
					if errMsg == "" {
						errMsg = "parameter null or not set"
					}
					fmt.Fprintf(os.Stderr, "lash: %s: %s\n", varName, errMsg)
					expandError = true
					return "", end - pos + 1
				}
				return val, end - pos + 1
			case ":substr":
				return expandSubstring(varName, val, operand), end - pos + 1
			}
		}

		return getVar(inner), end - pos + 1

	case next == '(':
		if pos+2 < len(s) && s[pos+2] == '(' {
			end, err := findMatchingDoubleParen(s, pos+2)
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				expandError = true
				return "", 0
			}
			expr := s[pos+3 : end]
			result := evalArithmetic(expr)
			return result, end + 2 - pos
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

func findMatchingBrace(s string, openPos int) (int, error) {
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
			if ch == '$' && i+1 < len(s) && s[i+1] == '{' {
				depth++
				i += 2
				continue
			}
			if ch == '}' {
				depth--
				if depth == 0 {
					return i, nil
				}
			}
		}

		i++
	}

	return -1, fmt.Errorf("unmatched ${")
}

func parseBraceExpansion(inner string) (varName, operand, op string) {
	if len(inner) == 0 {
		return "", "", ""
	}
	j := 0
	if isAlphaOrUnderscore(inner[0]) {
		j = 1
		for j < len(inner) && isAlnumOrUnderscore(inner[j]) {
			j++
		}
	}
	varName = inner[:j]

	if j+1 < len(inner) && inner[j] == ':' {
		switch inner[j+1] {
		case '-', '=', '+', '?':
			op = inner[j : j+2]
			operand = inner[j+2:]
			return varName, operand, op
		}
		op = ":substr"
		operand = inner[j+1:]
		return varName, operand, op
	}

	return varName, "", ""
}

type arithParser struct {
	expr []rune
	pos  int
}

func findMatchingDoubleParen(s string, openPos int) (int, error) {
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
					if i+1 < len(s) && s[i+1] == ')' {
						return i, nil
					}
				}
			}
		}
		i++
	}

	return -1, fmt.Errorf("unmatched $((")
}

func preprocessArithExpr(expr string) string {
	var result strings.Builder
	runes := []rune(expr)
	i := 0
	for i < len(runes) {
		if runes[i] == '$' && i+1 < len(runes) {
			if i+2 < len(runes) && runes[i+1] == '(' && runes[i+2] == '(' {
				end := i + 3
				depth := 1
				for end < len(runes) && depth > 0 {
					if end+1 < len(runes) && runes[end] == ')' && runes[end+1] == ')' {
						depth--
						if depth == 0 {
							end++
							break
						}
					} else if runes[end] == '(' {
						depth++
					}
					end++
				}
				inner := preprocessArithExpr(string(runes[i+3 : end-1]))
				result.WriteString(evalArithmetic(inner))
				i = end + 1
				continue
			}
			if runes[i+1] == '{' {
				end := i + 2
				for end < len(runes) && runes[end] != '}' {
					end++
				}
				if end < len(runes) {
					expanded := expandString(string(runes[i : end+1]))
					result.WriteString(expanded)
					i = end + 1
					continue
				}
			}
			if isAlphaOrUnderscore(byte(runes[i+1])) {
				j := i + 1
				for j < len(runes) && isAlnumOrUnderscore(byte(runes[j])) {
					j++
				}
				name := string(runes[i+1 : j])
				result.WriteString(getVar(name))
				i = j
				continue
			}
		}
		result.WriteRune(runes[i])
		i++
	}
	return result.String()
}

func evalArithmetic(expr string) string {
	expr = preprocessArithExpr(expr)
	runes := []rune(expr)
	p := &arithParser{expr: runes}
	val, _, err := p.parseComma()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: %s\n", err)
		expandError = true
		return "0"
	}
	p.skipSpaces()
	if p.pos < len(p.expr) {
		fmt.Fprintf(os.Stderr, "lash: unexpected token in arithmetic: %q\n", string(p.expr[p.pos]))
		expandError = true
		return "0"
	}
	return strconv.FormatInt(val, 10)
}

func (p *arithParser) peek() rune {
	p.skipSpaces()
	if p.pos >= len(p.expr) {
		return 0
	}
	return p.expr[p.pos]
}

func (p *arithParser) next() rune {
	if p.pos >= len(p.expr) {
		return 0
	}
	r := p.expr[p.pos]
	p.pos++
	return r
}

func (p *arithParser) skipSpaces() {
	for p.pos < len(p.expr) && p.expr[p.pos] == ' ' {
		p.pos++
	}
}

func (p *arithParser) matchToken(s string) bool {
	p.skipSpaces()
	runes := []rune(s)
	if p.pos+len(runes) > len(p.expr) {
		return false
	}
	for i, r := range runes {
		if p.expr[p.pos+i] != r {
			return false
		}
	}
	p.pos += len(runes)
	return true
}

func (p *arithParser) peekToken(s string) bool {
	p.skipSpaces()
	runes := []rune(s)
	if p.pos+len(runes) > len(p.expr) {
		return false
	}
	for i, r := range runes {
		if p.expr[p.pos+i] != r {
			return false
		}
	}
	return true
}

func (p *arithParser) parseComma() (int64, bool, error) {
	val, assigned, err := p.parseTernary()
	if err != nil {
		return 0, assigned, err
	}
	var a bool
	for p.peek() == ',' {
		p.pos++
		val, a, err = p.parseTernary()
		if err != nil {
			return 0, assigned || a, err
		}
		assigned = assigned || a
	}
	return val, assigned, nil
}

func (p *arithParser) parseTernary() (int64, bool, error) {
	cond, assigned, err := p.parseLogicalOr()
	if err != nil {
		return 0, assigned, err
	}
	p.skipSpaces()
	if p.pos < len(p.expr) && p.expr[p.pos] == '?' {
		p.pos++
		ifTrue, a, err := p.parseTernary()
		if err != nil {
			return 0, assigned || a, err
		}
		if !p.matchToken(":") {
			return 0, assigned || a, fmt.Errorf("expected ':' in ternary expression")
		}
		ifFalse, a, err := p.parseTernary()
		if err != nil {
			return 0, assigned || a, err
		}
		if cond != 0 {
			return ifTrue, assigned || a, nil
		}
		return ifFalse, assigned || a, nil
	}
	return cond, assigned, nil
}

func (p *arithParser) parseLogicalOr() (int64, bool, error) {
	left, assigned, err := p.parseLogicalAnd()
	if err != nil {
		return 0, assigned, err
	}
	for p.peekToken("||") {
		p.pos += 2
		right, a, err := p.parseLogicalAnd()
		if err != nil {
			return 0, assigned || a, err
		}
		assigned = assigned || a
		if left != 0 || right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseLogicalAnd() (int64, bool, error) {
	left, assigned, err := p.parseBitwiseOr()
	if err != nil {
		return 0, assigned, err
	}
	for p.peekToken("&&") {
		p.pos += 2
		right, a, err := p.parseBitwiseOr()
		if err != nil {
			return 0, assigned || a, err
		}
		assigned = assigned || a
		if left != 0 && right != 0 {
			left = 1
		} else {
			left = 0
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseBitwiseOr() (int64, bool, error) {
	left, assigned, err := p.parseBitwiseXor()
	if err != nil {
		return 0, assigned, err
	}
	for {
		ch := p.peek()
		if ch == '|' && !p.peekToken("||") {
			p.pos++
			right, a, err := p.parseBitwiseXor()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left |= right
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseBitwiseXor() (int64, bool, error) {
	left, assigned, err := p.parseBitwiseAnd()
	if err != nil {
		return 0, assigned, err
	}
	for {
		if p.peek() == '^' {
			p.pos++
			right, a, err := p.parseBitwiseAnd()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left ^= right
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseBitwiseAnd() (int64, bool, error) {
	left, assigned, err := p.parseEquality()
	if err != nil {
		return 0, assigned, err
	}
	for {
		ch := p.peek()
		if ch == '&' && !p.peekToken("&&") {
			p.pos++
			right, a, err := p.parseEquality()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left &= right
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseEquality() (int64, bool, error) {
	left, assigned, err := p.parseComparison()
	if err != nil {
		return 0, assigned, err
	}
	for {
		if p.peekToken("==") {
			p.pos += 2
			right, a, err := p.parseComparison()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if left == right {
				left = 1
			} else {
				left = 0
			}
		} else if p.peekToken("!=") {
			p.pos += 2
			right, a, err := p.parseComparison()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if left != right {
				left = 1
			} else {
				left = 0
			}
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseComparison() (int64, bool, error) {
	left, assigned, err := p.parseShift()
	if err != nil {
		return 0, assigned, err
	}
	for {
		if p.peekToken("<=") {
			p.pos += 2
			right, a, err := p.parseShift()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if left <= right {
				left = 1
			} else {
				left = 0
			}
		} else if p.peekToken(">=") {
			p.pos += 2
			right, a, err := p.parseShift()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if left >= right {
				left = 1
			} else {
				left = 0
			}
		} else if p.peek() == '<' && !p.peekToken("<<") {
			p.pos++
			right, a, err := p.parseShift()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if left < right {
				left = 1
			} else {
				left = 0
			}
		} else if p.peek() == '>' && !p.peekToken(">>") {
			p.pos++
			right, a, err := p.parseShift()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if left > right {
				left = 1
			} else {
				left = 0
			}
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseShift() (int64, bool, error) {
	left, assigned, err := p.parseAddition()
	if err != nil {
		return 0, assigned, err
	}
	for {
		if p.peekToken("<<") {
			p.pos += 2
			right, a, err := p.parseAddition()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left <<= right
		} else if p.peekToken(">>") {
			p.pos += 2
			right, a, err := p.parseAddition()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left >>= right
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseAddition() (int64, bool, error) {
	left, assigned, err := p.parseMultiplication()
	if err != nil {
		return 0, assigned, err
	}
	for {
		ch := p.peek()
		if ch == '+' && !p.peekToken("++") {
			p.pos++
			right, a, err := p.parseMultiplication()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left += right
		} else if ch == '-' && !p.peekToken("--") {
			p.pos++
			right, a, err := p.parseMultiplication()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left -= right
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseMultiplication() (int64, bool, error) {
	left, assigned, err := p.parseExponentiation()
	if err != nil {
		return 0, assigned, err
	}
	for {
		ch := p.peek()
		if ch == '*' && !p.peekToken("**") {
			p.pos++
			right, a, err := p.parseExponentiation()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			left *= right
		} else if ch == '/' {
			p.pos++
			right, a, err := p.parseExponentiation()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if right == 0 {
				fmt.Fprintln(os.Stderr, "lash: division by zero")
				expandError = true
				left = 0
			} else {
				left /= right
			}
		} else if ch == '%' {
			p.pos++
			right, a, err := p.parseExponentiation()
			if err != nil {
				return 0, assigned || a, err
			}
			assigned = assigned || a
			if right == 0 {
				fmt.Fprintln(os.Stderr, "lash: division by zero")
				expandError = true
				left = 0
			} else {
				left %= right
			}
		} else {
			break
		}
	}
	return left, assigned, nil
}

func (p *arithParser) parseExponentiation() (int64, bool, error) {
	base, assigned, err := p.parseUnary()
	if err != nil {
		return 0, assigned, err
	}
	if p.peekToken("**") {
		p.pos += 2
		exp, a, err := p.parseExponentiation()
		if err != nil {
			return 0, assigned || a, err
		}
		assigned = assigned || a
		if exp < 0 {
			if base == 1 {
				return 1, assigned, nil
			}
			if base == -1 {
				if exp%2 == 0 {
					return 1, assigned, nil
				}
				return -1, assigned, nil
			}
			return 0, assigned, nil
		}
		var result int64 = 1
		for i := int64(0); i < exp; i++ {
			if result != 0 && base > (1<<62)/result {
				result = 1 << 62
				break
			}
			result *= base
		}
		return result, assigned, nil
	}
	return base, assigned, nil
}

func (p *arithParser) parseUnary() (int64, bool, error) {
	ch := p.peek()
	if p.peekToken("++") {
		p.pos += 2
		p.skipSpaces()
		start := p.pos
		for p.pos < len(p.expr) && isAlnumOrUnderscore(byte(p.expr[p.pos])) {
			p.pos++
		}
		if p.pos == start {
			return 0, false, fmt.Errorf("expected identifier after '++'")
		}
		name := string(p.expr[start:p.pos])
		val := parseArithVar(name) + 1
		setVar(name, strconv.FormatInt(val, 10), false)
		return val, true, nil
	}
	if p.peekToken("--") {
		p.pos += 2
		p.skipSpaces()
		start := p.pos
		for p.pos < len(p.expr) && isAlnumOrUnderscore(byte(p.expr[p.pos])) {
			p.pos++
		}
		if p.pos == start {
			return 0, false, fmt.Errorf("expected identifier after '--'")
		}
		name := string(p.expr[start:p.pos])
		val := parseArithVar(name) - 1
		setVar(name, strconv.FormatInt(val, 10), false)
		return val, true, nil
	}
	if ch == '-' {
		p.pos++
		val, assigned, err := p.parseUnary()
		return -val, assigned, err
	}
	if ch == '+' {
		p.pos++
		return p.parseUnary()
	}
	if ch == '!' {
		p.pos++
		val, assigned, err := p.parseUnary()
		if err != nil {
			return 0, assigned, err
		}
		if val == 0 {
			return 1, assigned, nil
		}
		return 0, assigned, nil
	}
	if ch == '~' {
		p.pos++
		val, assigned, err := p.parseUnary()
		return ^val, assigned, err
	}
	return p.parseAssignment()
}

func (p *arithParser) parseAssignment() (int64, bool, error) {
	p.skipSpaces()
	start := p.pos
	if p.pos < len(p.expr) && isAlphaOrUnderscore(byte(p.expr[p.pos])) {
		p.pos++
		for p.pos < len(p.expr) && isAlnumOrUnderscore(byte(p.expr[p.pos])) {
			p.pos++
		}
		nameEnd := p.pos
		p.skipSpaces()
		if p.pos < len(p.expr) && p.expr[p.pos] == '=' && !p.peekToken("==") && !p.peekToken("!=") {
			name := string(p.expr[start:nameEnd])
			p.pos++
			val, _, err := p.parseAssignment()
			if err != nil {
				return 0, true, err
			}
			setVar(name, strconv.FormatInt(val, 10), false)
			return val, true, nil
		}
	}
	p.pos = start
	return p.parsePostfix()
}

func parseArithVar(name string) int64 {
	valStr := getVar(name)
	if valStr == "" {
		return 0
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

func (p *arithParser) parsePostfix() (int64, bool, error) {
	p.skipSpaces()
	start := p.pos
	if p.pos < len(p.expr) && isAlphaOrUnderscore(byte(p.expr[p.pos])) {
		p.pos++
		for p.pos < len(p.expr) && isAlnumOrUnderscore(byte(p.expr[p.pos])) {
			p.pos++
		}
		name := string(p.expr[start:p.pos])
		savePos := p.pos
		p.skipSpaces()
		if p.peekToken("++") {
			p.pos += 2
			orig := parseArithVar(name)
			setVar(name, strconv.FormatInt(orig+1, 10), false)
			return orig, true, nil
		}
		if p.peekToken("--") {
			p.pos += 2
			orig := parseArithVar(name)
			setVar(name, strconv.FormatInt(orig-1, 10), false)
			return orig, true, nil
		}
		p.pos = savePos
		return parseArithVar(name), false, nil
	}
	return p.parsePrimary()
}

func (p *arithParser) parsePrimary() (int64, bool, error) {
	ch := p.peek()

	if ch == '(' {
		p.pos++
		val, assigned, err := p.parseTernary()
		if err != nil {
			return 0, assigned, err
		}
		if p.peek() != ')' {
			return 0, assigned, fmt.Errorf("expected ')' in arithmetic expression")
		}
		p.pos++
		return val, assigned, nil
	}

	if ch >= '0' && ch <= '9' {
		start := p.pos
		for p.pos < len(p.expr) && p.expr[p.pos] >= '0' && p.expr[p.pos] <= '9' {
			p.pos++
		}
		if p.expr[start] == '0' && p.pos+1 <= len(p.expr) && p.pos < len(p.expr) && (p.expr[p.pos] == 'x' || p.expr[p.pos] == 'X') {
			p.pos++
			for p.pos < len(p.expr) && ((p.expr[p.pos] >= '0' && p.expr[p.pos] <= '9') || (p.expr[p.pos] >= 'a' && p.expr[p.pos] <= 'f') || (p.expr[p.pos] >= 'A' && p.expr[p.pos] <= 'F')) {
				p.pos++
			}
		}
		numStr := string(p.expr[start:p.pos])
		val, err := strconv.ParseInt(numStr, 0, 64)
		if err != nil {
			return 0, false, fmt.Errorf("invalid number: %s", numStr)
		}
		return val, false, nil
	}

	if isAlphaOrUnderscore(byte(ch)) {
		start := p.pos
		for p.pos < len(p.expr) && isAlnumOrUnderscore(byte(p.expr[p.pos])) {
			p.pos++
		}
		name := string(p.expr[start:p.pos])
		valStr := getVar(name)
		if valStr == "" {
			return 0, false, nil
		}
		val, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			return 0, false, nil
		}
		return val, false, nil
	}

	if ch == 0 {
		return 0, false, fmt.Errorf("unexpected end of arithmetic expression")
	}
	return 0, false, fmt.Errorf("unexpected token in arithmetic: %q", string(ch))
}

func expandSubstring(varName, value, operand string) string {
	operand = expandString(strings.TrimSpace(operand))
	colon := strings.Index(operand, ":")
	var offsetStr, lengthStr string
	if colon >= 0 {
		offsetStr = operand[:colon]
		lengthStr = operand[colon+1:]
	} else {
		offsetStr = operand
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: %s: %s: substring expression\n", varName, offsetStr)
		expandError = true
		return value
	}

	runes := []rune(value)
	length := len(runes)

	if offset < 0 {
		offset = length + offset
		if offset < 0 {
			offset = 0
		}
	} else if offset > length {
		return ""
	}

	if lengthStr != "" {
		l, err := strconv.Atoi(lengthStr)
		if err != nil {
			return string(runes[offset:])
		}
		if l < 0 {
			end := length + l
			if end <= offset {
				return ""
			}
			return string(runes[offset:end])
		}
		if offset+l > length {
			return string(runes[offset:])
		}
		return string(runes[offset : offset+l])
	}

	return string(runes[offset:])
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

func findMatchingProcSubstParen(s string, start int) int {
	depth := 1
	inSingle := false
	inDouble := false
	for i := start; i < len(s); i++ {
		if inSingle {
			if s[i] == '\'' {
				inSingle = false
			}
			continue
		}
		if s[i] == '\'' && !inDouble {
			inSingle = true
			continue
		}
		if s[i] == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if s[i] == '\\' && inDouble && i+1 < len(s) {
			i++
			continue
		}
		if s[i] == '(' && !inSingle && !inDouble {
			depth++
		}
		if s[i] == ')' && !inSingle && !inDouble {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isAlphaOrUnderscore(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isAlnumOrUnderscore(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

type procSubstEntry struct {
	cmd *exec.Cmd
	fd  *os.File
}

var procSubstEntries []procSubstEntry
var procSubstCount int
var procSubstMap map[string]*os.File

func init() {
	procSubstMap = make(map[string]*os.File)
}

func procSubstPlaceholder() string {
	id := procSubstCount
	procSubstCount++
	return "__LASH_PROCSUBST_" + strconv.Itoa(id) + "__"
}

func resolveProcSubstFile(path string) *os.File {
	if f, ok := procSubstMap[path]; ok {
		return f
	}
	return nil
}

func resolveProcSubstArgs(args []string) ([]string, []*os.File) {
	var extraFiles []*os.File
	resolved := make([]string, len(args))
	for i, arg := range args {
		if f, ok := procSubstMap[arg]; ok {
			extraFiles = append(extraFiles, f)
			resolved[i] = "/dev/fd/" + strconv.Itoa(3+len(extraFiles)-1)
		} else {
			resolved[i] = arg
		}
	}
	return resolved, extraFiles
}

func expandProcSubst(token string) string {
	if len(token) < 3 {
		return token
	}
	dir := token[0]
	if (dir != '<' && dir != '>') || token[1] != '(' || token[len(token)-1] != ')' {
		return token
	}
	innerCmd := token[2 : len(token)-1]
	innerCmd = strings.TrimSpace(innerCmd)
	if innerCmd == "" {
		fmt.Fprintf(os.Stderr, "lash: process substitution: empty command\n")
		lastExitCode = 2
		return ""
	}

	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: %s\n", err)
		lastExitCode = 1
		return ""
	}

	tokens := tokenize(innerCmd)
	if len(tokens) == 0 {
		r.Close()
		w.Close()
		return ""
	}

	expanded := expandVariables(tokens)
	segments := splitPipes(expanded)

	var cmd *exec.Cmd
	if len(segments) == 1 {
		cmdArgs, inFile, outFile, appendMode := parseRedirection(segments[0])
		if len(cmdArgs) == 0 {
			r.Close()
			w.Close()
			return ""
		}
		cmd = exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stderr = os.Stderr

		if dir == '<' {
			cmd.Stdout = w
			if inFile != "" {
				f, err := os.Open(inFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					r.Close()
					w.Close()
					return ""
				}
				cmd.Stdin = f
			}
		} else {
			cmd.Stdin = r
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
					r.Close()
					w.Close()
					return ""
				}
				cmd.Stdout = f
			} else {
				cmd.Stdout = os.Stdout
			}
		}
	} else {
		type pipePair struct {
			r *os.File
			w *os.File
		}
		pipes := make([]pipePair, len(segments)-1)
		for i := range pipes {
			pr, pw, err := os.Pipe()
			if err != nil {
				fmt.Fprintf(os.Stderr, "lash: %s\n", err)
				r.Close()
				w.Close()
				return ""
			}
			pipes[i] = pipePair{r: pr, w: pw}
		}

		var cmds []*exec.Cmd
		for i, seg := range segments {
			cmdArgs, inFile, outFile, appendMode := parseRedirection(seg)
			if len(cmdArgs) == 0 {
				r.Close()
				w.Close()
				for _, p := range pipes {
					p.r.Close()
					p.w.Close()
				}
				return ""
			}

			c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
			c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			c.Stderr = os.Stderr

			if inFile != "" {
				f, err := os.Open(inFile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "lash: %s\n", err)
					r.Close()
					w.Close()
					for _, p := range pipes {
						p.r.Close()
						p.w.Close()
					}
					return ""
				}
				c.Stdin = f
			} else if i == 0 {
				if dir == '<' {
					c.Stdin = os.Stdin
				} else {
					c.Stdin = r
				}
			} else {
				c.Stdin = pipes[i-1].r
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
					r.Close()
					w.Close()
					for _, p := range pipes {
						p.r.Close()
						p.w.Close()
					}
					return ""
				}
				c.Stdout = f
			} else if i == len(segments)-1 {
				if dir == '<' {
					c.Stdout = w
				} else {
					c.Stdout = os.Stdout
				}
			} else {
				c.Stdout = pipes[i].w
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
				r.Close()
				w.Close()
				for _, p := range pipes {
					p.r.Close()
					p.w.Close()
				}
				return ""
			}
		}

		for _, p := range pipes {
			p.w.Close()
		}

		go func() {
			for _, c := range cmds {
				var status syscall.WaitStatus
				for {
					_, err := syscall.Wait4(c.Process.Pid, &status, 0, nil)
					if err != syscall.EINTR {
						break
					}
				}
			}
			for _, p := range pipes {
				p.r.Close()
			}
			if dir == '<' {
				w.Close()
			} else {
				r.Close()
			}
		}()

		var keptFd *os.File
		if dir == '<' {
			keptFd = r
		} else {
			keptFd = w
		}
		placeholder := procSubstPlaceholder()
		procSubstEntries = append(procSubstEntries, procSubstEntry{cmd: cmds[0], fd: keptFd})
		procSubstMap[placeholder] = keptFd
		return placeholder
	}

	if err := cmd.Start(); err != nil {
		if _, ok := err.(*exec.Error); ok {
			fmt.Fprintf(os.Stderr, "lash: %s: command not found\n", cmd.Path)
			lastExitCode = 127
		} else {
			fmt.Fprintf(os.Stderr, "lash: %s\n", err)
			lastExitCode = 1
		}
		r.Close()
		w.Close()
		return ""
	}

	if dir == '<' {
		w.Close()
	} else {
		r.Close()
	}

	var keptFd *os.File
	if dir == '<' {
		keptFd = r
	} else {
		keptFd = w
	}
	placeholder := procSubstPlaceholder()
	procSubstEntries = append(procSubstEntries, procSubstEntry{cmd: cmd, fd: keptFd})
	procSubstMap[placeholder] = keptFd
	return placeholder
}

func waitProcSubst() {
	for _, entry := range procSubstEntries {
		entry.fd.Close()
		var status syscall.WaitStatus
		for {
			_, err := syscall.Wait4(entry.cmd.Process.Pid, &status, 0, nil)
			if err != syscall.EINTR {
				break
			}
		}
		if status.Exited() {
			if status.ExitStatus() != 0 {
				lastExitCode = status.ExitStatus()
			}
		}
	}
	procSubstEntries = nil
	for k := range procSubstMap {
		delete(procSubstMap, k)
	}
}
