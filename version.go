package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed logo/lash.txt
var logoBig string

//go:embed logo/lashsmall.txt
var logoSmall string

//go:embed logo/minilash.txt
var logoMini string

//go:embed ROADMAP.md
var roadmap string

func getVersion() string {
	type phaseData struct {
		number    int
		completed int
		total     int
	}

	var phases []phaseData
	currentIdx := -1
	scanner := bufio.NewScanner(strings.NewReader(roadmap))

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## Phase ") {
			var p int
			fmt.Sscanf(line, "## Phase %d", &p)
			if strings.Contains(line, "(current)") {
				currentIdx = len(phases)
			}
			phases = append(phases, phaseData{number: p})
			continue
		}
		if len(phases) == 0 {
			continue
		}
		last := &phases[len(phases)-1]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x]") {
			last.completed++
			last.total++
		} else if strings.HasPrefix(trimmed, "- [ ]") {
			last.total++
		}
	}

	idx := currentIdx
	for idx < len(phases) && phases[idx].total > 0 && phases[idx].completed == phases[idx].total {
		idx++
	}
	if idx >= len(phases) {
		idx = len(phases) - 1
	}

	return fmt.Sprintf("v%d.%d", phases[idx].number, phases[idx].completed)
}

func getLogo(size string) string {
	switch size {
	case "mini":
		return logoMini
	case "small":
		return logoSmall
	default:
		return logoBig
	}
}

func printVersion(size string) {
	fmt.Print(getLogo(size))
	fmt.Printf("\n              lash %s\n\n", getVersion())
}
