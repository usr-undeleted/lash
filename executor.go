package main

import (
	"strings"
)

func expandVariables(tokens []string) []string {
	expanded := make([]string, len(tokens))
	for i, t := range tokens {
		expanded[i] = expandString(t)
	}
	return expanded
}

func expandGlobs(tokens []string) []string {
	var result []string
	for _, t := range tokens {
		if hasExtGlob(t) {
			matches := matchExtGlob(t)
			if len(matches) > 0 {
				result = append(result, matches...)
			} else {
				result = append(result, restoreGlobMarkers(t))
			}
		} else if hasDoubleStar(t) {
			matches := globRecursive(t)
			if len(matches) > 0 {
				result = append(result, matches...)
			} else {
				result = append(result, restoreGlobMarkers(t))
			}
		} else if strings.ContainsAny(t, "*?[") {
			matches, err := customGlob(t)
			if err == nil && len(matches) > 0 {
				result = append(result, matches...)
			} else {
				result = append(result, restoreGlobMarkers(t))
			}
		} else {
			result = append(result, restoreGlobMarkers(t))
		}
	}
	return result
}

func buildEnvWithPrefix(prefix map[string]string) []string {
	varMu.Lock()
	var env []string
	seen := make(map[string]bool)
	for k, v := range prefix {
		env = append(env, k+"="+v)
		seen[k] = true
	}
	for key := range exportedVars {
		if !seen[key] {
			if val, ok := varTable[key]; ok {
				env = append(env, key+"="+val)
			}
		}
	}
	varMu.Unlock()
	return env
}
