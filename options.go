package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

type shellOption struct {
	name  string
	short string
	ptr   *bool
	def   bool
	desc  string
}

var (
	optionMu    sync.RWMutex
	optionTable = make(map[string]*shellOption)
)

func initOptions() {
	reg := func(name, short string, ptr *bool, def bool, desc string) {
		*ptr = def
		o := &shellOption{name: name, short: short, ptr: ptr, def: def, desc: desc}
		optionTable[name] = o
		if short != "" {
			optionTable[short] = o
		}
	}
	reg("errexit", "e", &setErrExit, false, "exit on error")
	reg("xtrace", "x", &setXTrace, false, "trace commands")
	reg("pipefail", "", &setPipefail, false, "pipeline fails if any cmd fails")
	reg("noclobber", "C", &setNoClobber, false, "refuse > on existing files")
	reg("nounset", "u", &setNoUnset, false, "error on unset variable")
	reg("noglob", "f", &setNoGlob, false, "disable glob expansion")
	reg("notify", "", &setNotify, false, "immediate job notifications")
	reg("hist_ignore_dups", "", &setHistIgnoreDups, false, "skip duplicate history entries")
	reg("hist_ignore_space", "", &setHistIgnoreSpace, false, "skip space-prefixed history entries")
	reg("huponexit", "", &setHupOnExit, false, "SIGHUP jobs on shell exit")
	reg("ignoreeof", "", &setIgnoreEOF, false, "require 10 Ctrl-D to exit")
	reg("hashall", "", &setHashAll, true, "hash command paths")
}

func getOption(name string) (*shellOption, bool) {
	optionMu.RLock()
	defer optionMu.RUnlock()
	o, ok := optionTable[name]
	return o, ok
}

func setOption(name string, enable bool) bool {
	o, ok := getOption(name)
	if !ok {
		return false
	}
	optionMu.Lock()
	*o.ptr = enable
	optionMu.Unlock()
	return true
}

func sortedOptions() []*shellOption {
	optionMu.RLock()
	defer optionMu.RUnlock()
	seen := make(map[string]bool)
	var list []*shellOption
	for _, o := range optionTable {
		if !seen[o.name] {
			seen[o.name] = true
			list = append(list, o)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].name < list[j].name
	})
	return list
}

func listOptions() {
	for _, o := range sortedOptions() {
		state := "off"
		if *o.ptr {
			state = "on"
		}
		short := ""
		if o.short != "" {
			short = fmt.Sprintf(" (-%s)", o.short)
		}
		fmt.Fprintf(os.Stdout, "%-20s %s%s  %s\n", o.name, state, short, o.desc)
	}
}

func listOptionsRestore() {
	for _, o := range sortedOptions() {
		if *o.ptr == o.def {
			continue
		}
		if *o.ptr {
			fmt.Fprintf(os.Stdout, "set -o %s\n", o.name)
		} else {
			fmt.Fprintf(os.Stdout, "set +o %s\n", o.name)
		}
	}
}

func validOptionNames() []string {
	var names []string
	for _, o := range sortedOptions() {
		names = append(names, o.name)
	}
	return names
}

func didYouMeanOption(input string) string {
	names := validOptionNames()
	best := ""
	bestDist := len(input) + 2
	for _, n := range names {
		d := levenshtein(input, n)
		if d < bestDist {
			bestDist = d
			best = n
		}
	}
	if bestDist <= 3 {
		return best
	}
	return ""
}

func builtinSetopt(args []string) {
	if len(args) == 1 {
		listOptions()
		lastExitCode = 0
		return
	}
	for _, name := range args[1:] {
		if strings.HasPrefix(name, "no") && len(name) > 2 {
			if o, ok := getOption(name[2:]); ok {
				optionMu.Lock()
				*o.ptr = false
				optionMu.Unlock()
				continue
			}
		}
		if ok := setOption(name, true); !ok {
			fmt.Fprintf(os.Stderr, "setopt: %s: invalid option\n", name)
			if hint := didYouMeanOption(name); hint != "" {
				fmt.Fprintf(os.Stderr, "setopt: did you mean: %s\n", hint)
			}
			lastExitCode = 1
		}
	}
}

func builtinUnsetopt(args []string) {
	if len(args) == 1 {
		fmt.Fprintln(os.Stderr, "unsetopt: option name required")
		lastExitCode = 2
		return
	}
	for _, name := range args[1:] {
		if ok := setOption(name, false); !ok {
			fmt.Fprintf(os.Stderr, "unsetopt: %s: invalid option\n", name)
			if hint := didYouMeanOption(name); hint != "" {
				fmt.Fprintf(os.Stderr, "unsetopt: did you mean: %s\n", hint)
			}
			lastExitCode = 1
		}
	}
}
