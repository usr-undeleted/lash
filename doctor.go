package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type doctorResult struct {
	ok  bool
	msg string
}

func runDoctor(cfg *Config, doFix bool) {
	results := map[string]doctorResult{
		"binary": checkBinary(doFix),
		"update": checkUpdateSource(cfg, doFix),
		"config": checkConfig(doFix),
		"themes": checkThemes(cfg, doFix),
		"shell":  checkShell(doFix),
	}

	failed := false
	for _, name := range []string{"binary", "update", "config", "themes", "shell"} {
		r := results[name]
		if r.ok {
			fmt.Printf("  \u2713 %-10s %s\n", name, r.msg)
		} else {
			fmt.Printf("  \u2717 %-10s %s\n", name, r.msg)
			failed = true
		}
	}

	if failed && !doFix {
		fmt.Println()
		fmt.Println("run lash doctor --fix to repair issues")
	}
	lastExitCode = 0
}

func checkBinary(doFix bool) doctorResult {
	self, err := os.Executable()
	if err != nil {
		return doctorResult{false, "cannot determine binary path"}
	}

	info, err := os.Stat(self)
	if err != nil {
		return doctorResult{false, self + " not found"}
	}

	if info.Mode()&0200 == 0 {
		if doFix {
			if err := os.Chmod(self, 0755); err != nil {
				return doctorResult{false, self + " not writable — fix failed: " + err.Error()}
			}
			return doctorResult{true, self + " (fixed)"}
		}
		return doctorResult{false, self + " not writable"}
	}

	return doctorResult{true, self}
}

func checkUpdateSource(cfg *Config, doFix bool) doctorResult {
	if cfg.UpdateSource == "" {
		if doFix {
			return doctorResult{false, "update-source not set — run: lash set-config update-source <path>"}
		}
		return doctorResult{false, "update-source not set"}
	}

	info, err := os.Stat(cfg.UpdateSource)
	if err != nil || !info.IsDir() {
		if doFix {
			return doctorResult{false, cfg.UpdateSource + " does not exist — run: lash set-config update-source <path>"}
		}
		return doctorResult{false, cfg.UpdateSource + " does not exist"}
	}

	gitDir := filepath.Join(cfg.UpdateSource, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return doctorResult{false, cfg.UpdateSource + " (not a git repo)"}
	}

	return doctorResult{true, cfg.UpdateSource}
}

func checkConfig(doFix bool) doctorResult {
	path := configPath()
	if path == "" {
		return doctorResult{false, "cannot determine config path"}
	}

	info, err := os.Stat(path)
	if err != nil {
		if doFix {
			if err := createDefaultConfig(path); err != nil {
				return doctorResult{false, "config missing — fix failed: " + err.Error()}
			}
			return doctorResult{true, path + " (created)"}
		}
		return doctorResult{false, path + " missing"}
	}

	if info.Size() == 0 {
		if doFix {
			if err := createDefaultConfig(path); err != nil {
				return doctorResult{false, "config empty — fix failed: " + err.Error()}
			}
			return doctorResult{true, path + " (rewritten)"}
		}
		return doctorResult{false, path + " empty"}
	}

	cfg := LoadConfig()
	if cfg == nil {
		return doctorResult{false, path + " unparseable"}
	}

	return doctorResult{true, path}
}

func checkThemes(cfg *Config, doFix bool) doctorResult {
	dir := themesDirPath()
	if dir == "" {
		return doctorResult{false, "cannot determine themes path"}
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		if doFix && cfg.UpdateSource != "" {
			srcDir := filepath.Join(cfg.UpdateSource, "themes")
			if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
				os.MkdirAll(dir, 0755)
				srcEntries, _ := os.ReadDir(srcDir)
				for _, e := range srcEntries {
					data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
					if err != nil {
						continue
					}
					os.WriteFile(filepath.Join(dir, e.Name()), data, 0644)
				}
				newCount, _ := os.ReadDir(dir)
				return doctorResult{true, fmt.Sprintf("%d themes (restored)", len(newCount))}
			}
			return doctorResult{false, "no themes — source unavailable"}
		}
		return doctorResult{false, "no themes installed"}
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			count++
		}
	}

	if count == 0 {
		return doctorResult{false, "no themes installed"}
	}

	return doctorResult{true, fmt.Sprintf("%d themes", count)}
}

func checkShell(doFix bool) doctorResult {
	self, err := os.Executable()
	if err != nil {
		return doctorResult{false, "cannot determine binary path"}
	}

	data, err := os.ReadFile("/etc/shells")
	if err != nil {
		return doctorResult{false, "cannot read /etc/shells"}
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == self {
			return doctorResult{true, "registered in /etc/shells"}
		}
	}

	if doFix {
		cmd := exec.Command("sudo", "sh", "-c", fmt.Sprintf("echo '%s' >> /etc/shells", self))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return doctorResult{false, "failed to register in /etc/shells: " + err.Error()}
		}
		return doctorResult{true, "registered in /etc/shells"}
	}

	return doctorResult{false, "not in /etc/shells"}
}

func createDefaultConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	content := "syntax-color = 1\nhistory-size = 1000\nauto-suggest = 1\n"
	return os.WriteFile(path, []byte(content), 0644)
}
