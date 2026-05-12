package main

import "strings"

// unescapePath removes backslash escapes from a path string.
// e.g. "projects/Modern\ Wars/" becomes "projects/Modern Wars/"
func unescapePath(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			buf.WriteByte(s[i+1])
			i += 2
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	return buf.String()
}
