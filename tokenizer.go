package main

import (
	"strings"
)

type chainEntry struct {
	operator string
	args     []string
}

func splitChains(line string) [][]chainEntry {
	var groups [][]chainEntry
	var current []chainEntry
	tokens := tokenize(line)
	i := 0
	for i < len(tokens) {
		if tokens[i] == ";" {
			if len(current) > 0 {
				groups = append(groups, current)
				current = nil
			}
			i++
			continue
		}
		var args []string
		for i < len(tokens) && tokens[i] != "&&" && tokens[i] != "||" && tokens[i] != ";" {
			args = append(args, tokens[i])
			i++
		}
		if len(args) > 0 {
			current = append(current, chainEntry{operator: "", args: args})
		}
		if i < len(tokens) && (tokens[i] == "&&" || tokens[i] == "||") {
			op := tokens[i]
			i++
			var nextArgs []string
			for i < len(tokens) && tokens[i] != "&&" && tokens[i] != "||" && tokens[i] != ";" {
				nextArgs = append(nextArgs, tokens[i])
				i++
			}
			if len(nextArgs) > 0 {
				current = append(current, chainEntry{operator: op, args: nextArgs})
			}
		}
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
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

	flushCurrent := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
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
			flushCurrent()
			tokens = append(tokens, string(ch))
		case '>':
			if i+1 < len(bytes) && bytes[i+1] == '>' {
				flushCurrent()
				tokens = append(tokens, ">>")
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
		case ';', '|':
			flushCurrent()
			tokens = append(tokens, string(ch))
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
					substDepth = 1
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
