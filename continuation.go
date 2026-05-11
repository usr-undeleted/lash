package main

func needsContinuation(buf []rune) bool {
	inSingle := false
	inDouble := false
	cmdSubDepth := 0
	arithDepth := 0
	var lastToken []rune
	var inToken bool
	var tokenStart int

	for i, r := range buf {
		if inSingle {
			if r == '\'' {
				inSingle = false
			}
			continue
		}
		if r == '\\' && inDouble && i+1 < len(buf) {
			continue
		}
		if r == '\\' && !inDouble && i+1 < len(buf) {
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = true
			continue
		}
		if r == '"' {
			inDouble = !inDouble
			continue
		}
		if r == '$' && i+1 < len(buf) && !inSingle {
			next := buf[i+1]
			if next == '(' && i+2 < len(buf) {
				if buf[i+2] == '(' {
					arithDepth++
					continue
				}
				cmdSubDepth++
				continue
			}
		}
		if cmdSubDepth > 0 && r == ')' {
			cmdSubDepth--
			continue
		}
		if arithDepth > 0 && r == ')' && i > 0 && buf[i-1] == ')' {
			arithDepth--
			continue
		}
		if inSingle || inDouble || cmdSubDepth > 0 || arithDepth > 0 {
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' {
			if inToken {
				inToken = false
				lastToken = buf[tokenStart:i]
			}
			continue
		}
		if r == '#' && !inToken {
			break
		}
		if !inToken {
			inToken = true
			tokenStart = i
		}
	}
	if inSingle || inDouble || cmdSubDepth > 0 || arithDepth > 0 {
		return true
	}
	if inToken {
		lastToken = buf[tokenStart:]
	}
	tok := string(lastToken)
	switch tok {
	case "|", "&&", "||", ";", ">", ">>", ">|", "<", "<<", "<<-", "<<<",
		"then", "do", "else", "elif", "in", "{", "(":
		return true
	}
	return false
}
