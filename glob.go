package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
		matches, err := filepath.Glob(pattern)
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
			addMatch(path)
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
	matches, err := filepath.Glob(fullPattern)
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
		subDir := filepath.Join(dir, e.Name())
		collectGlobMatches(subDir, suffixPattern, addMatch)
	}
}
