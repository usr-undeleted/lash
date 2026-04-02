package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	SyntaxColor  bool
	LogoSize     string
	HistorySize  int
	GlobDotfiles bool
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "lash", "config")
}

func LoadConfig() *Config {
	cfg := &Config{LogoSize: "big", HistorySize: 1000}
	path := configPath()
	if path == "" {
		return cfg
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "syntax-color":
			cfg.SyntaxColor = val == "1"
		case "logosize":
			cfg.LogoSize = val
		case "history-size":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				cfg.HistorySize = n
			}
		case "glob-dotfiles":
			cfg.GlobDotfiles = val == "1"
		}
	}
	return cfg
}

func (c *Config) Save() error {
	path := configPath()
	if path == "" {
		return fmt.Errorf("cannot determine config path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("syntax-color = %s", boolToStr(c.SyntaxColor)))
	lines = append(lines, fmt.Sprintf("logosize = %s", c.LogoSize))
	lines = append(lines, fmt.Sprintf("history-size = %d", c.HistorySize))
	lines = append(lines, fmt.Sprintf("glob-dotfiles = %s", boolToStr(c.GlobDotfiles)))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
	return nil
}

func (c *Config) Set(key, val string) bool {
	switch key {
	case "syntax-color":
		c.SyntaxColor = val == "1"
		return true
	case "logosize":
		switch val {
		case "mini", "small", "big":
			c.LogoSize = val
			return true
		}
		return false
	case "history-size":
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			c.HistorySize = n
			return true
		}
		return false
	case "glob-dotfiles":
		c.GlobDotfiles = val == "1"
		return true
	}
	return false
}

type configEntry struct {
	key   string
	usage string
	desc  string
}

var configKeys = []configEntry{
	{"syntax-color", "<0|1>", "highlight commands green/red as you type"},
	{"logosize", "<mini|small|big>", "logo size for lash version"},
	{"history-size", "<int>", "max number of history entries"},
	{"glob-dotfiles", "<0|1>", "include dotfiles in glob expansion"},
}

func printConfigList() {
	for _, e := range configKeys {
		fmt.Printf("%-15s = %-20s %s\n", e.key, e.usage, e.desc)
	}
}

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func isValidCommand(name string) bool {
	if isBuiltin(name) {
		return true
	}
	_, err := exec.LookPath(name)
	return err == nil
}
