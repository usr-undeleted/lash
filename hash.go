package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var hashTable map[string]string
var hashHits map[string]int
var hashMu sync.RWMutex
var lastPATH string
var hashScanDone bool

func initHashTable() {
	hashTable = make(map[string]string)
	hashHits = make(map[string]int)
	lastPATH = os.Getenv("PATH")
	hashScanDone = false
}

// lookup a command in the hash cache; falls back to exec.LookPath and caches the result
func hashLookup(name string) (string, bool) {
	if strings.Contains(name, "/") {
		return name, true
	}

	hashMu.Lock()
	defer hashMu.Unlock()

	hashPathChangedLocked()

	if path, ok := hashTable[name]; ok {
		hashHits[name]++
		return path, true
	}

	resolved, err := findExecutable(name)
	if err != nil {
		return "", false
	}

	if setHashAll {
		hashTable[name] = resolved
		hashHits[name] = 1
	}
	return resolved, true
}

// find an executable by scanning PATH (no caching)
func findExecutable(name string) (string, error) {
	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		full := filepath.Join(dir, name)
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return full, nil
		}
	}
	return "", &os.PathError{Op: "find", Path: name, Err: os.ErrNotExist}
}

// scan all PATH dirs and populate the hash table with every executable found
func hashScanPath() {
	hashMu.Lock()
	defer hashMu.Unlock()

	if hashScanDone {
		hashPathChangedLocked()
		if hashScanDone {
			return
		}
	}

	hashTable = make(map[string]string)
	hashHits = make(map[string]int)
	lastPATH = os.Getenv("PATH")

	path := os.Getenv("PATH")
	seen := make(map[string]bool)
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if seen[name] {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				seen[name] = true
				hashTable[name] = filepath.Join(dir, name)
				hashHits[name] = 0
			}
		}
	}

	hashScanDone = true
}

// clear the entire hash cache (called on PATH change or hash -r)
func hashClear() {
	hashMu.Lock()
	defer hashMu.Unlock()

	hashTable = make(map[string]string)
	hashHits = make(map[string]int)
	hashScanDone = false
	lastPATH = os.Getenv("PATH")
}

// delete specific entries from the hash cache
func hashDelete(names []string) {
	hashMu.Lock()
	defer hashMu.Unlock()

	for _, name := range names {
		delete(hashTable, name)
		delete(hashHits, name)
	}
}

// manually register a command path in the cache
func hashRegister(path, name string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}

	hashMu.Lock()
	defer hashMu.Unlock()

	hashTable[name] = path
	hashHits[name] = 0
	return true
}

// return sorted list of all cached command names
func hashNames() []string {
	hashMu.RLock()
	defer hashMu.RUnlock()

	names := make([]string, 0, len(hashTable))
	for name := range hashTable {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// check if PATH changed and clear cache if so (caller must hold hashMu lock)
func hashPathChangedLocked() {
	current := os.Getenv("PATH")
	if current != lastPATH {
		hashTable = make(map[string]string)
		hashHits = make(map[string]int)
		hashScanDone = false
		lastPATH = current
	}
}
