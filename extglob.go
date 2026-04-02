package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func hasExtGlob(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			continue
		}
		if i+1 < len(pattern) {
			two := pattern[i : i+2]
			if two == "?(" || two == "*(" || two == "+(" || two == "@(" || two == "!(" {
				return true
			}
		}
	}
	return false
}

func findExtGlobParen(s string, start int) int {
	depth := 0
	inSq := false
	for i := start; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '[' && !inSq {
			inSq = true
			continue
		}
		if s[i] == ']' && inSq {
			inSq = false
			continue
		}
		if inSq {
			continue
		}
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitExtGlobPatterns(inner string) []string {
	var parts []string
	depth := 0
	inSq := false
	start := 0
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' {
			i++
			continue
		}
		if inner[i] == '[' && !inSq {
			inSq = true
			continue
		}
		if inner[i] == ']' && inSq {
			inSq = false
			continue
		}
		if inSq {
			continue
		}
		if inner[i] == '(' {
			depth++
		} else if inner[i] == ')' {
			depth--
		} else if inner[i] == '|' && depth == 0 {
			parts = append(parts, inner[start:i])
			start = i + 1
		}
	}
	parts = append(parts, inner[start:])
	return parts
}

type negGroup struct {
	prefix string
	suffix string
	pats   []string
}

func extractNegGroups(pattern string) (string, []negGroup) {
	var clean strings.Builder
	var groups []negGroup
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			clean.WriteByte(pattern[i])
			i++
			clean.WriteByte(pattern[i])
			i++
			continue
		}
		if ch == '!' && i+1 < len(pattern) && pattern[i+1] == '(' {
			end := findExtGlobParen(pattern, i+1)
			if end != -1 {
				inner := pattern[i+2 : end]
				parts := splitExtGlobPatterns(inner)
				ng := negGroup{
					prefix: clean.String(),
					pats:   parts,
				}
				groups = append(groups, ng)
				clean.Reset()
				i = end + 1
				rest := pattern[i:]
				slashIdx := -1
				for j := 0; j < len(rest); j++ {
					if rest[j] == '/' {
						slashIdx = j
						break
					}
				}
				if slashIdx >= 0 {
					groups[len(groups)-1].suffix = rest[:slashIdx]
					clean.WriteString(rest[slashIdx:])
					i = len(pattern)
				} else {
					i = end + 1
				}
				continue
			}
		}
		clean.WriteByte(pattern[i])
		i++
	}
	return clean.String(), groups
}

func globToRegex(pattern string) string {
	var re strings.Builder
	re.WriteString("^")
	if currentConfig != nil && !currentConfig.GlobCaseSensitive {
		re.WriteString("(?i)")
	}
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			re.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
			i += 2
			continue
		}
		if ch == '?' && i+1 < len(pattern) && pattern[i+1] == '(' {
			end := findExtGlobParen(pattern, i+1)
			if end != -1 {
				inner := pattern[i+2 : end]
				parts := splitExtGlobPatterns(inner)
				var subRes []string
				for _, p := range parts {
					subRes = append(subRes, globToRegexPart(p))
				}
				re.WriteString("(" + strings.Join(subRes, "|") + ")?")
				i = end + 1
				continue
			}
			re.WriteString("[^/]")
			i++
			continue
		}
		if ch == '*' && i+1 < len(pattern) && pattern[i+1] == '(' {
			end := findExtGlobParen(pattern, i+1)
			if end != -1 {
				inner := pattern[i+2 : end]
				parts := splitExtGlobPatterns(inner)
				var subRes []string
				for _, p := range parts {
					subRes = append(subRes, globToRegexPart(p))
				}
				re.WriteString("(" + strings.Join(subRes, "|") + ")*")
				i = end + 1
				continue
			}
			re.WriteString("[^/]*")
			i++
			continue
		}
		if ch == '+' && i+1 < len(pattern) && pattern[i+1] == '(' {
			end := findExtGlobParen(pattern, i+1)
			if end != -1 {
				inner := pattern[i+2 : end]
				parts := splitExtGlobPatterns(inner)
				var subRes []string
				for _, p := range parts {
					subRes = append(subRes, globToRegexPart(p))
				}
				re.WriteString("(" + strings.Join(subRes, "|") + ")+")
				i = end + 1
				continue
			}
			re.WriteString(regexp.QuoteMeta(string(ch)))
			i++
			continue
		}
		if ch == '@' && i+1 < len(pattern) && pattern[i+1] == '(' {
			end := findExtGlobParen(pattern, i+1)
			if end != -1 {
				inner := pattern[i+2 : end]
				parts := splitExtGlobPatterns(inner)
				var subRes []string
				for _, p := range parts {
					subRes = append(subRes, globToRegexPart(p))
				}
				re.WriteString("(" + strings.Join(subRes, "|") + ")")
				i = end + 1
				continue
			}
			re.WriteString(regexp.QuoteMeta(string(ch)))
			i++
			continue
		}
		if ch == '*' {
			re.WriteString("[^/]*")
			i++
			continue
		}
		if ch == '?' {
			re.WriteString("[^/]")
			i++
			continue
		}
		if ch == '[' {
			j := i + 1
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j < len(pattern) {
				bracket := pattern[i : j+1]
				if len(bracket) > 1 && bracket[1] == '!' {
					re.WriteString("[" + bracket[1:])
				} else {
					re.WriteString(bracket)
				}
				i = j + 1
				continue
			}
			re.WriteString(regexp.QuoteMeta(string(ch)))
			i++
			continue
		}
		re.WriteString(regexp.QuoteMeta(string(ch)))
		i++
	}
	re.WriteString("$")
	return re.String()
}

func globToRegexPart(pattern string) string {
	var re strings.Builder
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			re.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
			i += 2
			continue
		}
		if ch == '*' {
			re.WriteString("[^/]*")
			i++
			continue
		}
		if ch == '?' {
			re.WriteString("[^/]")
			i++
			continue
		}
		if ch == '[' {
			j := i + 1
			if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j < len(pattern) {
				bracket := pattern[i : j+1]
				if len(bracket) > 1 && bracket[1] == '!' {
					re.WriteString("[" + bracket[1:])
				} else {
					re.WriteString(bracket)
				}
				i = j + 1
				continue
			}
			re.WriteString(regexp.QuoteMeta(string(ch)))
			i++
			continue
		}
		re.WriteString(regexp.QuoteMeta(string(ch)))
		i++
	}
	return re.String()
}

func matchExtGlob(pattern string) []string {
	dir := "."
	globPart := pattern

	if strings.Contains(pattern, "/") {
		lastSlash := strings.LastIndex(pattern, "/")
		dir = pattern[:lastSlash]
		if dir == "" {
			dir = "/"
		}
		globPart = pattern[lastSlash+1:]
	}

	_, negGroups := extractNegGroups(globPart)
	if len(negGroups) > 0 {
		return matchNegExtGlob(dir, globPart, negGroups)
	}

	regexStr := globToRegex(globPart)
	re, err := regexp.Compile(regexStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: invalid extglob pattern: %s\n", pattern)
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !includeDotfile(name) {
			continue
		}
		if re.MatchString(name) {
			if dir == "." {
				matches = append(matches, name)
			} else {
				matches = append(matches, filepath.Join(dir, name))
			}
		}
	}

	sort.Strings(matches)
	return matches
}

func matchNegExtGlob(dir string, globPart string, negGroups []negGroup) []string {
	ng := negGroups[0]

	var negRegexes []*regexp.Regexp
	for _, p := range ng.pats {
		full := ng.prefix + p + ng.suffix
		regexStr := "^" + globToRegexPart(full) + "$"
		if currentConfig != nil && !currentConfig.GlobCaseSensitive {
			regexStr = "(?i)" + regexStr
		}
		re, err := regexp.Compile(regexStr)
		if err == nil {
			negRegexes = append(negRegexes, re)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !includeDotfile(name) {
			continue
		}
		excluded := false
		for _, negRe := range negRegexes {
			if negRe.MatchString(name) {
				excluded = true
				break
			}
		}
		if !excluded {
			if dir == "." {
				matches = append(matches, name)
			} else {
				matches = append(matches, filepath.Join(dir, name))
			}
		}
	}

	sort.Strings(matches)
	return matches
}
