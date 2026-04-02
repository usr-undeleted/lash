package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func includeDotfile(name string) bool {
	if len(name) == 0 {
		return true
	}
	if name[0] != '.' {
		return true
	}
	if currentConfig != nil && currentConfig.GlobDotfiles {
		return true
	}
	return false
}

func customGlob(pattern string) ([]string, error) {
	dir := "."
	filePattern := pattern
	if strings.Contains(pattern, "/") {
		lastSlash := strings.LastIndex(pattern, "/")
		dir = pattern[:lastSlash]
		if dir == "" {
			dir = "/"
		}
		filePattern = pattern[lastSlash+1:]
	}

	regexStr := "^" + simpleGlobToRegex(filePattern) + "$"
	if currentConfig != nil && !currentConfig.GlobCaseSensitive {
		regexStr = "(?i)" + regexStr
	}
	re, err := regexp.Compile(regexStr)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %s", pattern)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
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
	return matches, nil
}

func simpleGlobToRegex(pattern string) string {
	var re strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) {
			re.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
			i++
			continue
		}
		switch ch {
		case '*':
			re.WriteString("[^/]*")
		case '?':
			re.WriteString("[^/]")
		case '[':
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
				i = j
				continue
			}
			re.WriteString(regexp.QuoteMeta(string(ch)))
		default:
			re.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	return re.String()
}

func hasDoubleStar(pattern string) bool {
	parts := splitPathComponents(pattern)
	for _, p := range parts {
		if p == "**" {
			return true
		}
	}
	return false
}

func splitPathComponents(pattern string) []string {
	if pattern == "" {
		return nil
	}
	return strings.Split(pattern, "/")
}

func globRecursive(pattern string) []string {
	parts := splitPathComponents(pattern)

	dsIndex := -1
	for i, p := range parts {
		if p == "**" {
			dsIndex = i
			break
		}
	}
	if dsIndex < 0 {
		matches, err := customGlob(pattern)
		if err != nil || len(matches) == 0 {
			return nil
		}
		return matches
	}

	prefix := strings.Join(parts[:dsIndex], "/")
	suffixParts := parts[dsIndex+1:]

	var result []string
	seen := make(map[string]bool)

	addMatch := func(p string) {
		clean := filepath.Clean(p)
		if !seen[clean] {
			seen[clean] = true
			result = append(result, clean)
		}
	}

	baseDir := prefix
	if baseDir == "" {
		baseDir = "."
	}

	if len(suffixParts) == 0 {
		filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if includeDotfile(d.Name()) {
				addMatch(path)
			}
			return nil
		})
	} else {
		suffixPattern := strings.Join(suffixParts, "/")
		collectGlobMatches(baseDir, suffixPattern, addMatch)
	}

	sort.Strings(result)
	return result
}

func collectGlobMatches(dir string, suffixPattern string, addMatch func(string)) {
	fullPattern := filepath.Join(dir, suffixPattern)
	matches, err := customGlob(fullPattern)
	if err == nil {
		for _, m := range matches {
			addMatch(m)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !includeDotfile(e.Name()) {
			continue
		}
		subDir := filepath.Join(dir, e.Name())
		collectGlobMatches(subDir, suffixPattern, addMatch)
	}
}
