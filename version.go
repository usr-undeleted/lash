package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed logo/lash.txt
var logoBig string

//go:embed logo/lashsmall.txt
var logoSmall string

//go:embed logo/minilash.txt
var logoMini string

//go:embed ROADMAP.md
var roadmap string

//go:embed README.md
var readme string

//go:embed descriptions/builtins.desc
var shippedDescs string

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
		if strings.HasPrefix(line, "## Phase ") && !strings.HasPrefix(line, "### ") {
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

	return fmt.Sprintf("v%d.%d%s", phases[idx].number, phases[idx].completed, getPatchVersion())
}

func getPatchVersion() string {
	scanner := bufio.NewScanner(strings.NewReader(readme))
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "version-v"); idx >= 0 {
			ver := line[idx+8:]
			end := len(ver)
			for i, c := range ver {
				if c == '-' {
					end = i
					break
				}
			}
			ver = ver[:end]
			parts := strings.Split(ver, ".")
			if len(parts) >= 3 {
				return "." + parts[2]
			}
			break
		}
	}
	return ""
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

func checkForUpdates(cfg *Config) {
	if cfg.UpdateSource == "" {
		return
	}

	cacheFile := filepath.Join(filepath.Dir(configPath()), "update-check")
	if data, err := os.ReadFile(cacheFile); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data))); err == nil {
			if time.Since(t) < 24*time.Hour {
				return
			}
		}
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	if err := os.Chdir(cfg.UpdateSource); err != nil {
		return
	}

	fetch := exec.Command("git", "fetch", "origin")
	done := make(chan struct{})
	go func() {
		fetch.Run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return
	}

	countOut, err := exec.Command("git", "rev-list", "HEAD..origin/main", "--count").Output()
	if err != nil {
		return
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOut)))
	if err != nil || count <= 0 {
		os.MkdirAll(filepath.Dir(cacheFile), 0755)
		os.WriteFile(cacheFile, []byte(time.Now().Format(time.RFC3339)), 0644)
		return
	}

	os.MkdirAll(filepath.Dir(cacheFile), 0755)
	os.WriteFile(cacheFile, []byte(time.Now().Format(time.RFC3339)), 0644)

	if count == 1 {
		fmt.Fprintf(os.Stderr, "lash: update available — run lash update\n")
	} else {
		fmt.Fprintf(os.Stderr, "lash: update available (%d commits behind) — run lash update\n", count)
	}
}
