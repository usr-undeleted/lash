package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

type ArgSpec struct {
	All     bool
	Null    bool
	Indices []int
}

type AliasSegment struct {
	Command   string
	Args      ArgSpec
	Separator string
}

type Alias struct {
	Name     string
	Raw      string
	Segments []AliasSegment
}

var (
	aliasTable map[string]*Alias
	aliasMu    sync.RWMutex
)

func initAliases() {
	aliasTable = make(map[string]*Alias)
}

func isAlias(name string) bool {
	aliasMu.RLock()
	defer aliasMu.RUnlock()
	_, ok := aliasTable[name]
	return ok
}

func allAliasNames() []string {
	aliasMu.RLock()
	defer aliasMu.RUnlock()
	names := make([]string, 0, len(aliasTable))
	for name := range aliasTable {
		names = append(names, name)
	}
	return names
}

func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') ||
			(s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseAliasDefinition(name, value string) (*Alias, error) {
	if !strings.Contains(value, "{") {
		return nil, fmt.Errorf("alias %s: missing argument specifier {ALL}, {NULL}, or {1,2,...}", name)
	}

	var segments []AliasSegment
	i := 0
	runes := []rune(value)

	for i < len(runes) {
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		if i >= len(runes) {
			break
		}

		cmdStart := i
		inSingle := false
		inDouble := false

		for i < len(runes) {
			ch := runes[i]
			if ch == '\'' && !inDouble {
				inSingle = !inSingle
				i++
				continue
			}
			if ch == '"' && !inSingle {
				inDouble = !inDouble
				i++
				continue
			}
			if !inSingle && !inDouble {
				if ch == '\\' && i+1 < len(runes) {
					i += 2
					continue
				}
				if ch == '{' || ch == ';' || ch == '&' {
					break
				}
			}
			i++
		}

		cmd := strings.TrimSpace(string(runes[cmdStart:i]))
		cmd = stripQuotes(cmd)
		if cmd == "" {
			break
		}

		if i >= len(runes) {
			return nil, fmt.Errorf("alias %s: segment %q is missing argument specifier {ALL}, {NULL}, or {1,2,...}", name, cmd)
		}

		if runes[i] == ';' || runes[i] == '&' {
			return nil, fmt.Errorf("alias %s: segment %q is missing argument specifier {ALL}, {NULL}, or {1,2,...}", name, cmd)
		}

		braceStart := i
		depth := 0
		for i < len(runes) {
			if runes[i] == '{' {
				depth++
			} else if runes[i] == '}' {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
			i++
		}

		if depth != 0 {
			return nil, fmt.Errorf("alias %s: unmatched { in argument specifier", name)
		}

		braceContent := string(runes[braceStart+1 : i-1])
		braceContent = strings.TrimSpace(braceContent)

		if braceContent == "" {
			return nil, fmt.Errorf("alias %s: empty {} is not valid, use {NULL} for no arguments or {ALL} for all", name)
		}

		var args ArgSpec
		parts := strings.Split(braceContent, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if strings.EqualFold(p, "ALL") {
				args.All = true
			} else if strings.EqualFold(p, "NULL") {
				args.Null = true
			} else {
				n, err := parseInt(p)
				if err != nil {
					return nil, fmt.Errorf("alias %s: invalid argument index %q, expected number, ALL, or NULL", name, p)
				}
				if n < 1 {
					return nil, fmt.Errorf("alias %s: argument index must be >= 1, got %d", name, n)
				}
				args.Indices = append(args.Indices, n)
			}
		}

		if args.All && (len(args.Indices) > 0 || args.Null) {
			return nil, fmt.Errorf("alias %s: {ALL} cannot be mixed with indices or NULL", name)
		}
		if args.Null && len(args.Indices) > 0 {
			return nil, fmt.Errorf("alias %s: {NULL} cannot be mixed with indices", name)
		}
		if !args.All && !args.Null && len(args.Indices) == 0 {
			return nil, fmt.Errorf("alias %s: empty argument specifier, use {NULL} for no arguments or {ALL} for all", name)
		}

		sep := ""
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		if i < len(runes) {
			if runes[i] == ';' {
				sep = ";"
				i++
			} else if i+1 < len(runes) && runes[i] == '&' && runes[i+1] == '&' {
				sep = "&&"
				i += 2
			}
		}

		segments = append(segments, AliasSegment{
			Command:   cmd,
			Args:      args,
			Separator: sep,
		})
	}

	return &Alias{
		Name:     name,
		Raw:      value,
		Segments: segments,
	}, nil
}

func parseInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	if len(s) == 0 {
		return 0, fmt.Errorf("not a number")
	}
	return n, nil
}

func expandAliasLine(line string) string {
	runes := []rune(line)
	firstWordEnd := -1

	if len(runes) == 0 {
		return line
	}

	quoted := false
	quoteChar := rune(0)
	for i, r := range runes {
		if !quoted && (r == '\'' || r == '"') {
			quoted = true
			quoteChar = r
			continue
		}
		if quoted && r == quoteChar {
			quoted = false
			continue
		}
		if !quoted && (r == ' ' || r == '\t') {
			firstWordEnd = i
			break
		}
	}

	if firstWordEnd == -1 {
		if quoted {
			return line
		}
		firstWordEnd = len(runes)
	}

	firstWord := string(runes[:firstWordEnd])

	aliasMu.RLock()
	a, ok := aliasTable[firstWord]
	aliasMu.RUnlock()

	if !ok {
		return line
	}

	rest := strings.TrimSpace(string(runes[firstWordEnd:]))
	var args []string
	if rest != "" {
		args = tokenize(rest)
	}

	var parts []string
	for _, seg := range a.Segments {
		var segArgs []string
		if seg.Args.All {
			segArgs = args
		} else if seg.Args.Null {
			segArgs = nil
		} else {
			for _, idx := range seg.Args.Indices {
				if idx-1 < len(args) {
					segArgs = append(segArgs, args[idx-1])
				} else {
					fmt.Fprintf(os.Stderr, "alias %s: argument %d not provided (only %d args given)\n", a.Name, idx, len(args))
					return ""
				}
			}
		}

		cmd := seg.Command
		if len(segArgs) > 0 {
			cmd += " " + strings.Join(segArgs, " ")
		}
		parts = append(parts, cmd)
	}

	var result string
	for i, p := range parts {
		if i > 0 {
			sep := a.Segments[i-1].Separator
			if sep == "" {
				sep = ";"
			}
			result += sep + " "
		}
		result += p
	}

	return result
}

func printAliases() {
	aliasMu.RLock()
	defer aliasMu.RUnlock()

	var names []string
	for name := range aliasTable {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fmt.Printf("alias %s='%s'\n", name, aliasTable[name].Raw)
	}
}

func removeAlias(name string) bool {
	aliasMu.Lock()
	defer aliasMu.Unlock()
	_, ok := aliasTable[name]
	if !ok {
		return false
	}
	delete(aliasTable, name)
	return true
}

func snapshotAliases() map[string]*Alias {
	aliasMu.RLock()
	defer aliasMu.RUnlock()
	s := make(map[string]*Alias, len(aliasTable))
	for k, v := range aliasTable {
		s[k] = v
	}
	return s
}

func restoreAliases(saved map[string]*Alias) {
	aliasMu.Lock()
	defer aliasMu.Unlock()
	aliasTable = make(map[string]*Alias, len(saved))
	for k, v := range saved {
		aliasTable[k] = v
	}
}
