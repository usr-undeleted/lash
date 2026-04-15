package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type trustStatus int

const (
	trustUnknown trustStatus = iota
	trustAllowed             // allow + hash matches
	trustChanged             // allow + hash mismatch
	trustDenied              // explicitly denied
)

type trustEntry struct {
	status string
	hash   string
}

var lastLashenvPath string
var lastLashenvHash string

func trustedEnvsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "lash", "trusted-envs")
}

func findLashenv(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for {
		candidate := filepath.Join(dir, ".lashenv")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
		if dir == "/" {
			break
		}
		dir = filepath.Dir(dir)
	}
	return "", false
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func loadTrustDB() map[string]trustEntry {
	db := make(map[string]trustEntry)
	path := trustedEnvsPath()
	if path == "" {
		return db
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return db
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		status, path, hash := parts[0], parts[1], parts[2]
		if status != "allow" && status != "deny" {
			continue
		}
		db[path] = trustEntry{status: status, hash: hash}
	}
	return db
}

func saveTrustDB(db map[string]trustEntry) error {
	path := trustedEnvsPath()
	if path == "" {
		return fmt.Errorf("cannot determine trusted-envs path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var lines []string
	for p, entry := range db {
		lines = append(lines, fmt.Sprintf("%s %s %s", entry.status, p, entry.hash))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func checkTrust(path string, hash string, db map[string]trustEntry) trustStatus {
	entry, ok := db[path]
	if !ok {
		return trustUnknown
	}
	if entry.status == "deny" {
		return trustDenied
	}
	if entry.hash != hash {
		return trustChanged
	}
	return trustAllowed
}

func loadLashenvFile(path string, cfg *Config) int {
	return sourceFile(path, cfg)
}

func tryLoadLashenv(cfg *Config) int {
	dir, err := os.Getwd()
	if err != nil {
		return 0
	}

	envPath, found := findLashenv(dir)
	if !found {
		lastLashenvPath = ""
		lastLashenvHash = ""
		return 0
	}

	if envPath == lastLashenvPath {
		return 0
	}

	hash, err := sha256File(envPath)
	if err != nil {
		return 0
	}

	db := loadTrustDB()
	status := checkTrust(envPath, hash, db)

	switch status {
	case trustAllowed:
		lastLashenvPath = envPath
		lastLashenvHash = hash
		return loadLashenvFile(envPath, cfg)
	case trustChanged:
		fmt.Fprintf(os.Stderr, "lash: .lashenv at %s changed since approval — run 'lash env allow' to re-approve\n", envPath)
		lastLashenvPath = envPath
		lastLashenvHash = hash
		return 0
	case trustDenied:
		lastLashenvPath = envPath
		lastLashenvHash = hash
		return 0
	case trustUnknown:
		fmt.Fprintf(os.Stderr, "lash: .lashenv found at %s — run 'lash env allow' to trust it\n", envPath)
		lastLashenvPath = envPath
		lastLashenvHash = hash
		return 0
	}
	return 0
}

func printEnvHelp() {
	fmt.Fprintln(os.Stderr, "usage: lash env <command> [args]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  refresh           reload .lashenv for current directory")
	fmt.Fprintln(os.Stderr, "  allow [path]      trust a .lashenv (default: find from cwd)")
	fmt.Fprintln(os.Stderr, "  deny [path]       deny a .lashenv (default: find from cwd)")
	fmt.Fprintln(os.Stderr, "  trusted           list all trusted/denied .lashenv files")
	fmt.Fprintln(os.Stderr, "  help              show this help")
}

func builtinEnvRefresh(cfg *Config) {
	lastLashenvPath = ""
	lastLashenvHash = ""
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: env refresh: %s\n", err)
		lastExitCode = 1
		return
	}
	envPath, found := findLashenv(dir)
	if !found {
		fmt.Fprintln(os.Stderr, "lash: env refresh: no .lashenv found")
		lastExitCode = 1
		return
	}
	hash, err := sha256File(envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: env refresh: %s\n", err)
		lastExitCode = 1
		return
	}
	db := loadTrustDB()
	status := checkTrust(envPath, hash, db)
	switch status {
	case trustAllowed:
		fmt.Fprintf(os.Stderr, "lash: env refresh: loading %s\n", envPath)
		lastLashenvPath = envPath
		lastLashenvHash = hash
		lastExitCode = loadLashenvFile(envPath, cfg)
	case trustChanged:
		fmt.Fprintf(os.Stderr, "lash: .lashenv at %s changed since approval — run 'lash env allow' to re-approve\n", envPath)
		lastLashenvPath = envPath
		lastLashenvHash = hash
		lastExitCode = 1
	case trustDenied:
		fmt.Fprintf(os.Stderr, "lash: env refresh: %s is denied\n", envPath)
		lastLashenvPath = envPath
		lastLashenvHash = hash
		lastExitCode = 1
	case trustUnknown:
		fmt.Fprintf(os.Stderr, "lash: .lashenv found at %s — run 'lash env allow' to trust it\n", envPath)
		lastLashenvPath = envPath
		lastLashenvHash = hash
		lastExitCode = 1
	}
}

func builtinEnvAllow(args []string) {
	var envPath string
	if len(args) > 0 {
		envPath = filepath.Clean(args[0])
	} else {
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: env allow: %s\n", err)
			lastExitCode = 1
			return
		}
		found, ok := findLashenv(dir)
		if !ok {
			fmt.Fprintln(os.Stderr, "lash: env allow: no .lashenv found in current directory or parents")
			lastExitCode = 1
			return
		}
		envPath = found
	}
	abs, err := filepath.Abs(envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: env allow: %s\n", err)
		lastExitCode = 1
		return
	}
	hash, err := sha256File(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: env allow: %s\n", err)
		lastExitCode = 1
		return
	}
	db := loadTrustDB()
	db[abs] = trustEntry{status: "allow", hash: hash}
	if err := saveTrustDB(db); err != nil {
		fmt.Fprintf(os.Stderr, "lash: env allow: %s\n", err)
		lastExitCode = 1
		return
	}
	fmt.Fprintf(os.Stderr, "lash: trusted %s\n", abs)
	lastExitCode = 0
}

func builtinEnvDeny(args []string) {
	var envPath string
	if len(args) > 0 {
		envPath = filepath.Clean(args[0])
	} else {
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "lash: env deny: %s\n", err)
			lastExitCode = 1
			return
		}
		found, ok := findLashenv(dir)
		if !ok {
			fmt.Fprintln(os.Stderr, "lash: env deny: no .lashenv found in current directory or parents")
			lastExitCode = 1
			return
		}
		envPath = found
	}
	abs, err := filepath.Abs(envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: env deny: %s\n", err)
		lastExitCode = 1
		return
	}
	hash, err := sha256File(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lash: env deny: %s\n", err)
		lastExitCode = 1
		return
	}
	db := loadTrustDB()
	db[abs] = trustEntry{status: "deny", hash: hash}
	if err := saveTrustDB(db); err != nil {
		fmt.Fprintf(os.Stderr, "lash: env deny: %s\n", err)
		lastExitCode = 1
		return
	}
	fmt.Fprintf(os.Stderr, "lash: denied %s\n", abs)
	lastExitCode = 0
}

func builtinEnvTrusted() {
	db := loadTrustDB()
	if len(db) == 0 {
		fmt.Fprintln(os.Stderr, "lash: env trusted: no entries")
		lastExitCode = 0
		return
	}
	paths := make([]string, 0, len(db))
	for p := range db {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		entry := db[p]
		shortHash := entry.hash
		if len(shortHash) > 12 {
			shortHash = shortHash[:12]
		}
		fmt.Fprintf(os.Stderr, "%-6s %-12s %s\n", entry.status, shortHash, p)
	}
	lastExitCode = 0
}
