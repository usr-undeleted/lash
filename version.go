package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed logo/lash.txt
var logo string

//go:embed ROADMAP.md
var roadmap string

func getVersion() string {
	var phase int
	var completed int
	scanner := bufio.NewScanner(strings.NewReader(roadmap))
	foundCurrent := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## Phase ") && !foundCurrent {
			var p int
			n, err := fmt.Sscanf(line, "## Phase %d", &p)
			if err == nil && n == 1 {
				phase = p
			}
			if strings.Contains(line, "(current)") {
				foundCurrent = true
			}
			continue
		}
		if foundCurrent {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- [x]") {
				completed++
			}
			if strings.HasPrefix(line, "## Phase ") {
				break
			}
		}
	}
	return fmt.Sprintf("v%d.%d", phase, completed)
}

func printVersion() {
	fmt.Print(logo)
	fmt.Printf("\n              lash %s\n\n", getVersion())
}
