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
	SyntaxColor       bool
	LogoSize          string
	HistorySize       int
	GlobDotfiles      bool
	GlobCaseSensitive bool
	Theme             string
	ErrExit           bool
	XTrace            bool
	Pipefail          bool
	NoClobber         bool
	NoUnset           bool
	NoGlob            bool
	Notify            bool
	HistIgnoreDups    bool
	HistIgnoreSpace   bool
	HupOnExit         bool
	IgnoreEOF         bool
	HashAll           bool
	Keybinds          map[string]string
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "lash", "config")
}

func themesDirPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "lash", "themes")
}

func LoadConfig() *Config {
	cfg := &Config{LogoSize: "big", HistorySize: 1000, GlobCaseSensitive: true, Keybinds: make(map[string]string)}
	path := configPath()
	if path == "" {
		return cfg
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg
	}
	defer f.Close()

	inKeybinds := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[keybinds]" {
			inKeybinds = true
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inKeybinds = false
			continue
		}
		if inKeybinds {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if isValidKey(key) && isValidAction(val) {
					cfg.Keybinds[key] = val
				}
			}
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
		case "glob-case-sensitivity":
			cfg.GlobCaseSensitive = val != "0"
		case "theme":
			cfg.Theme = val
		case "errexit":
			cfg.ErrExit = val == "1"
		case "xtrace":
			cfg.XTrace = val == "1"
		case "pipefail":
			cfg.Pipefail = val == "1"
		case "noclobber":
			cfg.NoClobber = val == "1"
		case "nounset":
			cfg.NoUnset = val == "1"
		case "noglob":
			cfg.NoGlob = val == "1"
		case "notify":
			cfg.Notify = val == "1"
		case "hist-ignore-dups":
			cfg.HistIgnoreDups = val == "1"
		case "hist-ignore-space":
			cfg.HistIgnoreSpace = val == "1"
		case "huponexit":
			cfg.HupOnExit = val == "1"
		case "ignoreeof":
			cfg.IgnoreEOF = val == "1"
		case "hashall":
			cfg.HashAll = val == "1"
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
	lines = append(lines, fmt.Sprintf("glob-case-sensitivity = %s", boolToStr(c.GlobCaseSensitive)))
	if c.Theme != "" {
		lines = append(lines, fmt.Sprintf("theme = %s", c.Theme))
	}
	lines = append(lines, fmt.Sprintf("errexit = %s", boolToStr(c.ErrExit)))
	lines = append(lines, fmt.Sprintf("xtrace = %s", boolToStr(c.XTrace)))
	lines = append(lines, fmt.Sprintf("pipefail = %s", boolToStr(c.Pipefail)))
	lines = append(lines, fmt.Sprintf("noclobber = %s", boolToStr(c.NoClobber)))
	lines = append(lines, fmt.Sprintf("nounset = %s", boolToStr(c.NoUnset)))
	lines = append(lines, fmt.Sprintf("noglob = %s", boolToStr(c.NoGlob)))
	lines = append(lines, fmt.Sprintf("notify = %s", boolToStr(c.Notify)))
	lines = append(lines, fmt.Sprintf("hist-ignore-dups = %s", boolToStr(c.HistIgnoreDups)))
	lines = append(lines, fmt.Sprintf("hist-ignore-space = %s", boolToStr(c.HistIgnoreSpace)))
	lines = append(lines, fmt.Sprintf("huponexit = %s", boolToStr(c.HupOnExit)))
	lines = append(lines, fmt.Sprintf("ignoreeof = %s", boolToStr(c.IgnoreEOF)))
	lines = append(lines, fmt.Sprintf("hashall = %s", boolToStr(c.HashAll)))
	if len(c.Keybinds) > 0 {
		lines = append(lines, "")
		lines = append(lines, "[keybinds]")
		for _, k := range sortedKeys(c.Keybinds) {
			lines = append(lines, fmt.Sprintf("%s = %s", k, c.Keybinds[k]))
		}
	}
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
	case "glob-case-sensitivity":
		c.GlobCaseSensitive = val != "0"
		return true
	case "errexit":
		c.ErrExit = val == "1"
		return true
	case "xtrace":
		c.XTrace = val == "1"
		return true
	case "pipefail":
		c.Pipefail = val == "1"
		return true
	case "noclobber":
		c.NoClobber = val == "1"
		return true
	case "nounset":
		c.NoUnset = val == "1"
		return true
	case "noglob":
		c.NoGlob = val == "1"
		return true
	case "notify":
		c.Notify = val == "1"
		return true
	case "hist-ignore-dups":
		c.HistIgnoreDups = val == "1"
		return true
	case "hist-ignore-space":
		c.HistIgnoreSpace = val == "1"
		return true
	case "huponexit":
		c.HupOnExit = val == "1"
		return true
	case "ignoreeof":
		c.IgnoreEOF = val == "1"
		return true
	case "hashall":
		c.HashAll = val == "1"
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
	{"glob-case-sensitivity", "<0|1>", "case-sensitive (1) or insensitive (0) glob matching"},
	{"errexit", "<0|1>", "exit immediately if a command exits non-zero"},
	{"xtrace", "<0|1>", "print commands before execution"},
	{"pipefail", "<0|1>", "pipeline fails if any command in it fails"},
	{"noclobber", "<0|1>", "refuse > on existing files (use >| to force)"},
	{"nounset", "<0|1>", "error on unset variable expansion"},
	{"noglob", "<0|1>", "disable glob expansion"},
	{"notify", "<0|1>", "report background job status immediately"},
	{"hist-ignore-dups", "<0|1>", "skip duplicate consecutive history entries"},
	{"hist-ignore-space", "<0|1>", "skip history entries starting with space"},
	{"huponexit", "<0|1>", "send SIGHUP to all jobs when shell exits"},
	{"ignoreeof", "<0|1>", "require 10 Ctrl-D presses to exit"},
	{"hashall", "<0|1>", "hash command paths"},
}

func printConfigList() {
	for _, e := range configKeys {
		fmt.Printf("%-15s = %-20s %s\n", e.key, e.usage, e.desc)
	}
}

func printConfigShow(c *Config) {
	fmt.Printf("%-22s %s\n", "syntax-color", boolToStr(c.SyntaxColor))
	fmt.Printf("%-22s %s\n", "logosize", c.LogoSize)
	fmt.Printf("%-22s %d\n", "history-size", c.HistorySize)
	fmt.Printf("%-22s %s\n", "glob-dotfiles", boolToStr(c.GlobDotfiles))
	fmt.Printf("%-22s %s\n", "glob-case-sensitivity", boolToStr(c.GlobCaseSensitive))
	fmt.Printf("%-22s %s\n", "errexit", boolToStr(c.ErrExit))
	fmt.Printf("%-22s %s\n", "xtrace", boolToStr(c.XTrace))
	fmt.Printf("%-22s %s\n", "pipefail", boolToStr(c.Pipefail))
	fmt.Printf("%-22s %s\n", "noclobber", boolToStr(c.NoClobber))
	fmt.Printf("%-22s %s\n", "nounset", boolToStr(c.NoUnset))
	fmt.Printf("%-22s %s\n", "noglob", boolToStr(c.NoGlob))
	fmt.Printf("%-22s %s\n", "notify", boolToStr(c.Notify))
	fmt.Printf("%-22s %s\n", "hist-ignore-dups", boolToStr(c.HistIgnoreDups))
	fmt.Printf("%-22s %s\n", "hist-ignore-space", boolToStr(c.HistIgnoreSpace))
	fmt.Printf("%-22s %s\n", "huponexit", boolToStr(c.HupOnExit))
	fmt.Printf("%-22s %s\n", "ignoreeof", boolToStr(c.IgnoreEOF))
	fmt.Printf("%-22s %s\n", "hashall", boolToStr(c.HashAll))
}

func boolToStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func isValidCommand(name string) bool {
	if isBuiltin(name) || isKeyword(name) || isAlias(name) {
		return true
	}
	_, err := exec.LookPath(name)
	return err == nil
}
