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
	result := 	FindBestMatch(allBuiltins, typoLower, threshold)
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
	result = FindBestMatch(funcNames, typoLower, threshold)
	if result != "" {
		return result
	}

	// CWD directories (higher priority than PATH)
	if autocd {
		entries, rdErr := os.ReadDir(".")
		if rdErr == nil {
			var dirs []string
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && startsWithRune(e.Name(), rune(typoLower[0])) {
					dirs = append(dirs, e.Name()+"/")
				}
			}
			result = FindBestMatch(dirs, typoLower, threshold)
			if result != "" {
				return result
			}
		}
	}

	// Full PATH scan
	candidates := collectCandidates(autocd, rune(typoLower[0]))
	result = 	FindBestMatch(candidates, typoLower, threshold)
	return result
}

func FindBestMatch(candidates []string, typoLower string, threshold int) string {
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

		dist := damerauLevenshtein(typoLower, strings.ToLower(c))
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

func damerauLevenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	n, m := len(ar), len(br)
	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}

	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d[i][j] = min3(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				if d[i-2][j-2]+cost < d[i][j] {
					d[i][j] = d[i-2][j-2] + cost
				}
			}
		}
	}
	return d[n][m]
}
