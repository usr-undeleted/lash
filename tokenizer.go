package main

import "strings"

func tokenize(line string) []string {
	inSingle := false
	inDouble := false
	substDepth := 0
	substInSingle := false
	substInDouble := false
	procSubstDepth := 0
	procSubstInSingle := false
	procSubstInDouble := false
	extGlobDepth := 0

	var current strings.Builder
	var tokens []string
	bytes := []byte(line)
	inDollarBrace := false

	flushCurrent := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
			inDollarBrace = false
		}
	}

	for i := 0; i < len(bytes); i++ {
		ch := rune(bytes[i])

		if extGlobDepth > 0 {
			current.WriteByte(bytes[i])
			if bytes[i] == '(' {
				extGlobDepth++
			} else if bytes[i] == ')' {
				extGlobDepth--
			}
			continue
		}

		if procSubstDepth > 0 {
			if procSubstInSingle {
				if bytes[i] == '\'' {
					procSubstInSingle = false
				}
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\'' && !procSubstInDouble {
				procSubstInSingle = true
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '"' && !procSubstInSingle {
				procSubstInDouble = !procSubstInDouble
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\\' && procSubstInDouble && i+1 < len(bytes) {
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '(' && !procSubstInSingle && !procSubstInDouble {
				procSubstDepth++
			}
			if bytes[i] == ')' && !procSubstInSingle && !procSubstInDouble {
				procSubstDepth--
				if procSubstDepth == 0 {
					current.WriteByte(bytes[i])
					continue
				}
			}
			current.WriteByte(bytes[i])
			continue
		}

		if substDepth > 0 {
			if substInSingle {
				if bytes[i] == '\'' {
					substInSingle = false
				}
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\'' && !substInDouble {
				substInSingle = true
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '"' && !substInSingle {
				substInDouble = !substInDouble
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '\\' && substInDouble && i+1 < len(bytes) {
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			if bytes[i] == '(' && !substInSingle && !substInDouble {
				substDepth++
			}
			if bytes[i] == ')' && !substInSingle && !substInDouble {
				substDepth--
				if substDepth == 0 {
					current.WriteByte(bytes[i])
					continue
				}
			}
			current.WriteByte(bytes[i])
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteByte(bytes[i])
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteByte(bytes[i])
			continue
		}

		if inSingle || inDouble {
			current.WriteByte(bytes[i])
			continue
		}

		if ch == '\\' && i+1 < len(bytes) {
			current.WriteByte(bytes[i])
			i++
			current.WriteByte(bytes[i])
			continue
		}

		if ch == ' ' || ch == '\t' {
			flushCurrent()
			continue
		}

		if ch == '\n' {
			flushCurrent()
			tokens = append(tokens, ";")
			continue
		}

		if ch == '(' && i+1 < len(bytes) && bytes[i+1] == '(' {
			flushCurrent()
			tokens = append(tokens, "((")
			i++
			i++
			var content strings.Builder
			depth := 1
			for i < len(bytes) && depth > 0 {
				if bytes[i] == '(' && i+1 < len(bytes) && bytes[i+1] == '(' {
					depth++
					content.WriteByte('(')
					i++
					content.WriteByte('(')
					i++
					continue
				}
				if bytes[i] == ')' && i+1 < len(bytes) && bytes[i+1] == ')' {
					depth--
					if depth == 0 {
						i++
						break
					}
					content.WriteByte(')')
					i++
					content.WriteByte(')')
					i++
					continue
				}
				content.WriteByte(bytes[i])
				i++
			}
			tokens = append(tokens, content.String())
			continue
		}

		if ch == '(' {
			flushCurrent()
			tokens = append(tokens, "(")
			continue
		}

		if (ch == '?' || ch == '*' || ch == '+' || ch == '@' || ch == '!') && i+1 < len(bytes) && bytes[i+1] == '(' {
			current.WriteByte(bytes[i])
			i++
			current.WriteByte(bytes[i])
			extGlobDepth = 1
			continue
		}

		switch ch {
		case '<':
			if i+1 < len(bytes) && bytes[i+1] == '(' {
				flushCurrent()
				procSubstDepth = 1

				procSubstInSingle = false
				procSubstInDouble = false
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			if i+1 < len(bytes) && bytes[i+1] == '<' {
				if i+2 < len(bytes) && bytes[i+2] == '<' {
					flushCurrent()
					tokens = append(tokens, "<<<")
					i += 2
				} else if i+2 < len(bytes) && bytes[i+2] == '-' {
					flushCurrent()
					tokens = append(tokens, "<<-")
					i += 2
				} else {
					flushCurrent()
					tokens = append(tokens, "<<")
					i++
				}
			} else {
				flushCurrent()
				tokens = append(tokens, string(ch))
			}
		case '>':
			if i+1 < len(bytes) && bytes[i+1] == '>' {
				flushCurrent()
				tokens = append(tokens, ">>")
				i++
			} else if i+1 < len(bytes) && bytes[i+1] == '|' {
				flushCurrent()
				tokens = append(tokens, ">|")
				i++
			} else if i+1 < len(bytes) && bytes[i+1] == '(' {
				flushCurrent()
				procSubstDepth = 1

				procSubstInSingle = false
				procSubstInDouble = false
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			} else {
				flushCurrent()
				tokens = append(tokens, ">")
			}
		case ';':
			if i+1 < len(bytes) && bytes[i+1] == ';' {
				flushCurrent()
				tokens = append(tokens, ";;")
				i++
			} else {
				flushCurrent()
				tokens = append(tokens, ";")
			}
		case ')':
			flushCurrent()
			tokens = append(tokens, ")")
		case '{':
			if current.Len() > 0 && current.String()[current.Len()-1] == '$' {
				current.WriteByte(bytes[i])
				inDollarBrace = true
			} else {
				flushCurrent()
				tokens = append(tokens, "{")
			}
		case '}':
			if inDollarBrace {
				current.WriteByte(bytes[i])
				inDollarBrace = false
			} else {
				flushCurrent()
				tokens = append(tokens, "}")
			}
		case '|':
			if i+1 < len(bytes) && bytes[i+1] == '|' {
				flushCurrent()
				tokens = append(tokens, "||")
				i++
			} else {
				flushCurrent()
				tokens = append(tokens, "|")
			}
		case '&':
			if i+1 < len(bytes) && bytes[i+1] == '&' {
				flushCurrent()
				tokens = append(tokens, "&&")
				i++
			} else {
				flushCurrent()
				tokens = append(tokens, "&")
			}
		case '$':
			if i+1 < len(bytes) && bytes[i+1] == '(' {
				if i+2 < len(bytes) && bytes[i+2] == '(' {
					substDepth = 2
					current.WriteByte(bytes[i])
					i++
					current.WriteByte(bytes[i])
					i++
					current.WriteByte(bytes[i])
					continue
				}
				substDepth = 1
				current.WriteByte(bytes[i])
				i++
				current.WriteByte(bytes[i])
				continue
			}
			current.WriteByte(bytes[i])
		case '`':
			inBacktick := true
			current.WriteByte(bytes[i])
			for i++; i < len(bytes) && inBacktick; i++ {
				if bytes[i] == '\\' && i+1 < len(bytes) {
					current.WriteByte(bytes[i])
					i++
					current.WriteByte(bytes[i])
					continue
				}
				if bytes[i] == '`' {
					inBacktick = false
				}
				current.WriteByte(bytes[i])
			}
			i--
		default:
			current.WriteByte(bytes[i])
		}
	}
	flushCurrent()
	return tokens
}
