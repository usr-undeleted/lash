package main

func needsContinuation(buf []rune) bool {
	inSingle := false
	inDouble := false
	cmdSubDepth := 0
	arithDepth := 0

	var lastToken []rune
	var inToken bool
	var tokenStart int

	blockStack := 0

	processToken := func(end int) {
		if inToken && end > tokenStart {
			tok := string(buf[tokenStart:end])
			switch tok {
			case "if", "while", "until", "for", "case", "select", "{", "(":
				blockStack++
			case "fi", "done", "esac", "}", ")":
				if blockStack > 0 {
					blockStack--
				}
			}
			lastToken = buf[tokenStart:end]
		}
		inToken = false
	}

	for i, r := range buf {
		if inSingle {
			if r == '\'' {
				inSingle = false
			}
			continue
		}
		if r == '\\' && inDouble && i+1 < len(buf) {
			i++
			continue
		}
		if r == '\\' && !inDouble && i+1 < len(buf) {
			i++
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
		if arithDepth > 0 && i > 0 && r == ')' && buf[i-1] == ')' {
			arithDepth--
			continue
		}
		if inDouble || cmdSubDepth > 0 || arithDepth > 0 {
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' {
			processToken(i)
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
	processToken(len(buf))
	if inSingle || inDouble || cmdSubDepth > 0 || arithDepth > 0 {
		return true
	}
	tok := string(lastToken)
	switch tok {
	case "|", "&&", "||", ";", ">", ">>", ">|", "<", "<<", "<<-", "<<<",
		"then", "do", "else", "elif", "in", "{", "(":
		return true
	}
	if blockStack > 0 {
		return true
	}
	return false
}
