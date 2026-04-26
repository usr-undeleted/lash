package main

import (
	"os"
	"strings"
)

func collectCandidates(autocd bool, firstRune rune) []string {
	seen := make(map[string]bool)
	var candidates []string

	firstLower := firstRune
	if firstRune >= 'A' && firstRune <= 'Z' {
		firstLower = firstRune + 32
	}

	for _, cmd := range allBuiltins {
		if !seen[cmd] && startsWithRune(cmd, firstLower) {
			seen[cmd] = true
			candidates = append(candidates, cmd)
		}
	}

	funcMu.Lock()
	for name := range funcTable {
		if !seen[name] && startsWithRune(name, firstLower) {
			seen[name] = true
			candidates = append(candidates, name)
		}
	}
	funcMu.Unlock()

	hashScanPath()
	hashMu.RLock()
	for name := range hashTable {
		if !seen[name] && startsWithRune(name, firstLower) {
			seen[name] = true
			candidates = append(candidates, name)
		}
	}
	hashMu.RUnlock()

	if autocd {
		entries, err := os.ReadDir(".")
		if err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					name := e.Name() + "/"
					if !seen[name] && startsWithRune(name, firstLower) {
						seen[name] = true
						candidates = append(candidates, name)
					}
				}
			}
		}
	}

	return candidates
}

func startsWithRune(s string, r rune) bool {
	if len(s) == 0 {
		return false
	}
	first := rune(s[0])
	if first >= 'A' && first <= 'Z' {
		first += 32
	}
	return first == r
}

func findCorrection(typo string, threshold int, autocd bool) string {
	if len(typo) == 0 {
		return ""
	}

	typoLower := strings.ToLower(typo)

	// Try builtins first — small, high-confidence set
	result := findBestMatch(allBuiltins, typoLower, threshold)
	if result != "" {
		return result
	}

	// Try functions
	funcMu.Lock()
	var funcNames []string
	for name := range funcTable {
		funcNames = append(funcNames, name)
	}
	funcMu.Unlock()
	result = findBestMatch(funcNames, typoLower, threshold)
	if result != "" {
		return result
	}

	// Full PATH scan
	candidates := collectCandidates(autocd, rune(typoLower[0]))
	result = findBestMatch(candidates, typoLower, threshold)
	return result
}

func findBestMatch(candidates []string, typoLower string, threshold int) string {
	bestDist := threshold + 1
	var bestMatch string
	tied := false

	for _, c := range candidates {
		if c == typoLower || strings.EqualFold(c, typoLower) {
			return ""
		}

		clen := len([]rune(c))
		tlen := len([]rune(typoLower))
		if abs(clen-tlen) > bestDist {
			continue
		}

		dist := levenshtein(typoLower, strings.ToLower(c))
		if dist <= threshold {
			if dist < bestDist {
				bestDist = dist
				bestMatch = c
				tied = false
			} else if dist == bestDist {
				tied = true
			}
		}
	}

	if tied {
		return ""
	}
	return bestMatch
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
