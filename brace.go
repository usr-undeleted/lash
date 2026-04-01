package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

func expandBraces(tokens []string) []string {
	var result []string
	for _, t := range tokens {
		expanded := expandBraceToken(t)
		result = append(result, expanded...)
	}
	return result
}

func expandBraceToken(token string) []string {
	braceStart, braceEnd, hasComma, hasRange := findBraceExpansion(token)
	if braceStart < 0 {
		return []string{token}
	}

	prefix := token[:braceStart]
	suffix := token[braceEnd+1:]
	content := token[braceStart+1 : braceEnd]

	var alternatives []string

	if hasRange {
		alternatives = expandRange(content)
		if alternatives == nil {
			return []string{token}
		}
	} else if hasComma {
		alternatives = splitBraceElements(content)
	} else {
		return []string{token}
	}

	var results []string
	for _, alt := range alternatives {
		combined := prefix + alt + suffix
		expanded := expandBraceToken(combined)
		results = append(results, expanded...)
	}
	return results
}

type braceScanState struct {
	inSingle bool
	inDouble bool
}

func findBraceExpansion(token string) (braceStart, braceEnd int, hasComma, hasRange bool) {
	braceStart = -1
	braceEnd = -1
	var state braceScanState

	for i := 0; i < len(token); {
		ch, size := utf8.DecodeRuneInString(token[i:])
		if ch == utf8.RuneError {
			break
		}

		if state.inSingle {
			if ch == '\'' {
				state.inSingle = false
			}
			i += size
			continue
		}

		if state.inDouble {
			if ch == '"' {
				state.inDouble = false
			} else if ch == '\\' && i+size < len(token) {
				next, ns := utf8.DecodeRuneInString(token[i+size:])
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					i += size + ns
					continue
				}
			}
			i += size
			continue
		}

		if ch == '\'' {
			state.inSingle = true
			i += size
			continue
		}
		if ch == '"' {
			state.inDouble = true
			i += size
			continue
		}
		if ch == '\\' && i+size < len(token) {
			i += size * 2
			continue
		}

		if ch == '{' {
			start, end, comma, rng, found := tryBraceAt(token, i)
			if found {
				return start, end, comma, rng
			}
		}

		i += size
	}
	return -1, -1, false, false
}

func tryBraceAt(token string, openPos int) (braceStart, braceEnd int, hasComma, hasRange bool, found bool) {
	depth := 0
	comma := false
	isRange := false
	var state braceScanState

	i := openPos
	for i < len(token) {
		ch, size := utf8.DecodeRuneInString(token[i:])
		if ch == utf8.RuneError {
			return 0, 0, false, false, false
		}

		if state.inSingle {
			if ch == '\'' {
				state.inSingle = false
			}
			i += size
			continue
		}
		if state.inDouble {
			if ch == '"' {
				state.inDouble = false
			} else if ch == '\\' && i+size < len(token) {
				next, ns := utf8.DecodeRuneInString(token[i+size:])
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					i += size + ns
					continue
				}
			}
			i += size
			continue
		}

		if ch == '\'' {
			state.inSingle = true
			i += size
			continue
		}
		if ch == '"' {
			state.inDouble = true
			i += size
			continue
		}
		if ch == '\\' && i+size < len(token) {
			i += size * 2
			continue
		}

		if ch == '{' {
			depth++
			i += size
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				if !comma && !isRange {
					return 0, 0, false, false, false
				}
				return openPos, i, comma, isRange, true
			}
			i += size
			continue
		}
		if ch == ',' && depth == 1 {
			comma = true
		}
		if ch == '.' && depth == 1 {
			if i+size < len(token) {
				next, _ := utf8.DecodeRuneInString(token[i+size:])
				if next == '.' {
					isRange = true
				}
			}
		}
		i += size
	}

	return 0, 0, false, false, false
}

func splitBraceElements(content string) []string {
	var elements []string
	var current strings.Builder
	depth := 0
	var state braceScanState

	for i := 0; i < len(content); {
		ch, size := utf8.DecodeRuneInString(content[i:])
		if ch == utf8.RuneError {
			current.WriteRune(ch)
			i += size
			continue
		}

		if state.inSingle {
			current.WriteRune(ch)
			if ch == '\'' {
				state.inSingle = false
			}
			i += size
			continue
		}
		if state.inDouble {
			current.WriteRune(ch)
			if ch == '"' {
				state.inDouble = false
			} else if ch == '\\' && i+size < len(content) {
				next, ns := utf8.DecodeRuneInString(content[i+size:])
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					current.WriteRune(ch)
					current.WriteRune(next)
					i += size + ns
					continue
				}
			}
			i += size
			continue
		}

		if ch == '\'' {
			state.inSingle = true
			current.WriteRune(ch)
			i += size
			continue
		}
		if ch == '"' {
			state.inDouble = true
			current.WriteRune(ch)
			i += size
			continue
		}
		if ch == '\\' && i+size < len(content) {
			current.WriteRune(ch)
			next, ns := utf8.DecodeRuneInString(content[i+size:])
			current.WriteRune(next)
			i += size + ns
			continue
		}

		if ch == '{' {
			depth++
			current.WriteRune(ch)
		} else if ch == '}' {
			depth--
			current.WriteRune(ch)
		} else if ch == ',' && depth == 0 {
			elements = append(elements, current.String())
			current.Reset()
		} else {
			current.WriteRune(ch)
		}
		i += size
	}

	elements = append(elements, current.String())
	return elements
}

func expandRange(content string) []string {
	parts := strings.SplitN(content, "..", 2)
	if len(parts) != 2 {
		return nil
	}

	rest := parts[1]
	var step int = 1
	if idx := strings.Index(rest, ".."); idx >= 0 {
		stepStr := rest[idx+2:]
		rest = rest[:idx]
		var err error
		step, err = strconv.Atoi(stepStr)
		if err != nil || step == 0 {
			return nil
		}
	}

	startStr := parts[0]
	endStr := rest

	startInt, startErr := strconv.Atoi(startStr)
	endInt, endErr := strconv.Atoi(endStr)

	if startErr == nil && endErr == nil {
		return expandIntRange(startInt, endInt, step, startStr)
	}

	if isAlpha(startStr) && isAlpha(endStr) && utf8.RuneCountInString(startStr) == 1 && utf8.RuneCountInString(endStr) == 1 {
		sr, _ := utf8.DecodeRuneInString(startStr)
		er, _ := utf8.DecodeRuneInString(endStr)
		return expandCharRange(sr, er, step)
	}

	return nil
}

func expandIntRange(start, end, step int, origStart string) []string {
	var results []string
	padLen := 0
	if len(origStart) > 1 && origStart[0] == '0' {
		padLen = len(origStart)
	}

	if start <= end {
		for i := start; i <= end; i += step {
			results = append(results, padInt(i, padLen))
		}
	} else {
		for i := start; i >= end; i -= step {
			results = append(results, padInt(i, padLen))
		}
	}
	return results
}

func expandCharRange(start, end rune, step int) []string {
	var results []string
	if start <= end {
		for i := 0; start+rune(i*step) <= end; i++ {
			results = append(results, string(start+rune(i*step)))
		}
	} else {
		for i := 0; start-rune(i*step) >= end; i++ {
			results = append(results, string(start-rune(i*step)))
		}
	}
	return results
}

func padInt(n, width int) string {
	s := fmt.Sprintf("%d", n)
	if width > 0 && len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	return s
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return utf8.RuneCountInString(s) > 0
}
