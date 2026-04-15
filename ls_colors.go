package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var lsColorsMap map[string]string
var lsColorsMu sync.RWMutex
var lastLSColors string

func initLSColors() {
	lsColorsMap = make(map[string]string)
	lastLSColors = os.Getenv("LS_COLORS")
	lsColorsMap = parseLSColors(lastLSColors)
}

func parseLSColors(env string) map[string]string {
	m := make(map[string]string)
	if env == "" {
		return defaultLSColors()
	}
	for _, part := range strings.Split(env, ":") {
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		key := part[:eq]
		val := part[eq+1:]
		m[key] = val
	}

	for k, v := range defaultLSColors() {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return m
}

func defaultLSColors() map[string]string {
	return map[string]string{
		"rs": "0",
		"di": "01;34",
		"ln": "01;36",
		"mh": "00",
		"pi": "40;33",
		"so": "01;35",
		"do": "01;35",
		"bd": "40;33;01",
		"cd": "40;33;01",
		"or": "40;31;01",
		"mi": "00",
		"su": "37;41",
		"sg": "30;43",
		"ca": "30;41",
		"tw": "30;42",
		"ow": "34;42",
		"st": "37;44",
		"ex": "01;32",
		"fi": "00",
	}
}

func sgrToANSI(sgr string) string {
	if sgr == "" || sgr == "0" {
		return colorReset
	}
	return "\x1b[" + sgr + "m"
}

func lsColorsRefresh() {
	lsColorsMu.Lock()
	defer lsColorsMu.Unlock()
	current := os.Getenv("LS_COLORS")
	if current != lastLSColors {
		lsColorsMap = parseLSColors(current)
		lastLSColors = current
	}
}

func colorizeEntry(name string, mode os.FileMode, symlinkTarget string) string {
	lsColorsMu.RLock()
	defer lsColorsMu.RUnlock()

	color := ""
	found := false

	if mode&os.ModeSymlink != 0 {
		if symlinkTarget != "" {
			color = lsColorsMap["ln"]
			found = true
		} else {
			color = lsColorsMap["or"]
			found = true
		}
	}

	if !found && mode.IsDir() {
		color = lsColorsMap["di"]
		found = true
	}

	if !found && mode&os.ModeNamedPipe != 0 {
		color = lsColorsMap["pi"]
		found = true
	}

	if !found && mode&os.ModeSocket != 0 {
		color = lsColorsMap["so"]
		found = true
	}

	if !found && mode&os.ModeDevice != 0 {
		if mode&os.ModeCharDevice != 0 {
			color = lsColorsMap["cd"]
		} else {
			color = lsColorsMap["bd"]
		}
		found = true
	}

	if !found && mode&os.ModeSetuid != 0 {
		color = lsColorsMap["su"]
		found = true
	}

	if !found && mode&os.ModeSetgid != 0 {
		color = lsColorsMap["sg"]
		found = true
	}

	if !found && mode.IsRegular() && mode&0111 != 0 {
		color = lsColorsMap["ex"]
		found = true
	}

	if !found {
		base := filepath.Ext(name)
		if base != "" {
			if c, ok := lsColorsMap["*"+base]; ok {
				color = c
				found = true
			}
		}
	}

	if !found {
		if mode.IsDir() {
			color = lsColorsMap["tw"]
			if mode&0002 != 0 {
				color = lsColorsMap["ow"]
			}
			if mode&01000 != 0 {
				color = lsColorsMap["st"]
			}
		} else {
			color = lsColorsMap["fi"]
		}
	}

	if color == "" || color == "0" {
		return name
	}

	return sgrToANSI(color) + name + colorReset
}
