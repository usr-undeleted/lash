package main

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type descEntry struct {
	desc      string
	category  string
	flagChars []string
	flags     map[string]string
}

var (
	descTable map[string]*descEntry
	descMu    sync.RWMutex
)

func initDescriptions() {
	descMu.Lock()
	defer descMu.Unlock()
	descTable = make(map[string]*descEntry)
}

// parse a single .desc file and return its entries
func parseDescFile(content string) map[string]*descEntry {
	entries := make(map[string]*descEntry)
	scanner := bufio.NewScanner(strings.NewReader(content))
	var current *descEntry

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := trimmed[1 : len(trimmed)-1]
			name = strings.TrimSpace(name)
			if name == "" {
				current = nil
				continue
			}
			current = &descEntry{flags: make(map[string]string)}
			entries[name] = current
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(trimmed, "desc ") {
			val, ok := extractQuotedValue(trimmed[5:])
			if ok {
				current.desc = val
			}
			continue
		}

		if strings.HasPrefix(trimmed, "category ") {
			val, ok := extractQuotedValue(trimmed[9:])
			if ok {
				current.category = val
			}
			continue
		}

		if strings.HasPrefix(trimmed, "flag-char ") {
			val, ok := extractQuotedValue(trimmed[10:])
			if ok {
				current.flagChars = append(current.flagChars, val)
			}
			continue
		}

		if strings.HasPrefix(trimmed, "flag ") {
			rest := strings.TrimSpace(trimmed[5:])
			flagStr, desc, ok := parseFlagLine(rest)
			if ok {
				current.flags[flagStr] = desc
			}
			continue
		}
	}

	return entries
}

// extract a double-quoted value from a string, skipping leading whitespace
func extractQuotedValue(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' {
		return "", false
	}
	end := strings.IndexByte(s[1:], '"')
	if end < 0 {
		return "", false
	}
	return s[1 : end+1], true
}

// parse "flag_value" "description" from a flag line
func parseFlagLine(s string) (string, string, bool) {
	var flagStr string

	if len(s) > 0 && s[0] == '"' {
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return "", "", false
		}
		flagStr = s[1 : end+1]
		s = strings.TrimSpace(s[end+2:])
	} else {
		idx := strings.IndexByte(s, ' ')
		if idx < 0 {
			return "", "", false
		}
		flagStr = s[:idx]
		s = strings.TrimSpace(s[idx+1:])
	}

	desc, ok := extractQuotedValue(s)
	if !ok {
		return flagStr, "", true
	}
	return flagStr, desc, true
}

// load shipped descriptions from embedded content
func loadShippedDescriptions(efs embed.FS) {
	descMu.Lock()
	defer descMu.Unlock()
	entries, err := fs.ReadDir(efs, "descriptions")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".desc") {
			continue
		}
		data, err := fs.ReadFile(efs, "descriptions/"+entry.Name())
		if err != nil {
			continue
		}
		parsed := parseDescFile(string(data))
		for k, v := range parsed {
			descTable[k] = v
		}
	}
}

// load user descriptions from ~/.config/lash/descriptions/*.desc
func loadUserDescriptions() {
	home := os.Getenv("HOME")
	if home == "" {
		return
	}
	dir := filepath.Join(home, ".config", "lash", "descriptions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".desc") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsed := parseDescFile(string(data))
		descMu.Lock()
		for k, v := range parsed {
			if _, exists := descTable[k]; !exists {
				descTable[k] = v
			}
		}
		descMu.Unlock()
	}
}

// get description for a command name
func getDesc(name string) string {
	descMu.RLock()
	defer descMu.RUnlock()
	if e, ok := descTable[name]; ok {
		return e.desc
	}
	return ""
}

// get the descEntry for a command
func getDescEntry(name string) *descEntry {
	descMu.RLock()
	defer descMu.RUnlock()
	if e, ok := descTable[name]; ok {
		return e
	}
	return nil
}

// check if a character triggers flag completion for a given command
func commandHasFlagChar(cmd, ch string) bool {
	descMu.RLock()
	defer descMu.RUnlock()
	e, ok := descTable[cmd]
	if !ok {
		return false
	}
	for _, fc := range e.flagChars {
		if fc == ch {
			return true
		}
	}
	return false
}

// get flag completions for a command matching a prefix
func getFlagCompletions(cmd, prefix string) []completionEntry {
	descMu.RLock()
	defer descMu.RUnlock()
	e, ok := descTable[cmd]
	if !ok || len(e.flags) == 0 {
		return nil
	}
	var results []completionEntry
	for flag, desc := range e.flags {
		if strings.HasPrefix(flag, prefix) {
			results = append(results, completionEntry{name: flag, desc: desc})
		}
	}
	sortCompletionEntries(results)
	return results
}

func sortCompletionEntries(entries []completionEntry) {
	for i := 0; i < len(entries)-1; i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].name > entries[j].name {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}

func reloadDescriptions() {
	descMu.Lock()
	descTable = make(map[string]*descEntry)
	descMu.Unlock()
	loadShippedDescriptions(shippedDescFS)
	loadUserDescriptions()
}

func printDescInfo() {
	descMu.RLock()
	defer descMu.RUnlock()
	fmt.Printf("Loaded %d command descriptions\n", len(descTable))
	home := os.Getenv("HOME")
	if home != "" {
		userDir := filepath.Join(home, ".config", "lash", "descriptions")
		if info, err := os.Stat(userDir); err == nil && info.IsDir() {
			fmt.Printf("User descriptions directory: %s\n", userDir)
		} else {
			fmt.Printf("User descriptions directory: %s (not found)\n", userDir)
		}
	}
}
