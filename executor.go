package main

import (
	"os"
	"strconv"
	"strings"
)

const atSentinel = "\x00LASH_AT\x00"
const arrayAtSentinel = "\x00LASH_ARRAY_AT\x00"

func expandVariables(tokens []string) []string {
	var result []string
	for _, t := range tokens {
		expanded := expandString(t)
		if strings.Contains(expanded, atSentinel) {
			result = expandAtSentinel(expanded, result)
		} else if strings.Contains(expanded, arrayAtSentinel) {
			result = expandArrayAtSentinel(expanded, result)
		} else {
			result = append(result, expanded)
		}
	}
	return result
}

func expandAtSentinel(expanded string, result []string) []string {
	parts := strings.Split(expanded, atSentinel)
	if len(parts) == 0 {
		return result
	}
	first := true
	for _, part := range parts {
		if first {
			if part != "" {
				result = append(result, part)
			}
			first = false
			continue
		}
		for _, p := range positionalParams {
			if part != "" {
				result = append(result, part+p)
			} else {
				result = append(result, p)
			}
		}
	}
	return result
}

func expandArrayAtSentinel(expanded string, result []string) []string {
	parts := strings.Split(expanded, arrayAtSentinel)
	if len(parts) == 0 {
		return result
	}
	first := true
	for _, part := range parts {
		if first {
			if part != "" {
				result = append(result, part)
			}
			first = false
			continue
		}
		arrName := part
		elements := getArrayAll(arrName)
		if len(elements) == 0 {
			continue
		}
		for _, elem := range elements {
			result = append(result, elem)
		}
	}
	return result
}

func getArgMax() int {
	data, err := os.ReadFile("/proc/sys/kernel/arg_max")
	if err != nil {
		return 2097152
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n <= 0 {
		return 2097152
	}
	return n
}

func expandGlobs(tokens []string) []string {
	if setNoGlob {
		var result []string
		for _, t := range tokens {
			result = append(result, restoreGlobMarkers(t))
		}
		return result
	}
	safeMax := getArgMax() / 2
	var result []string
	totalSize := 0
	for _, t := range tokens {
		var expanded []string
		if hasExtGlob(t) {
			expanded = matchExtGlob(t)
		} else if hasDoubleStar(t) {
			expanded = globRecursive(t)
		} else if strings.ContainsAny(t, "*?[") {
			expanded, _ = customGlob(t)
		}
		if len(expanded) == 0 {
			literal := restoreGlobMarkers(t)
			result = append(result, literal)
			totalSize += len(literal) + 1
			continue
		}
		expSize := 0
		for _, e := range expanded {
			expSize += len(e) + 1
		}
		if totalSize+expSize > safeMax {
			result = append(result, restoreGlobMarkers(t))
		} else {
			result = append(result, expanded...)
			totalSize += expSize
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
