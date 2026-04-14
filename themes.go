package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func printThemeHelp() {
	fmt.Fprintln(os.Stderr, "usage: lash theme <command> [args]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  set <name>    apply a theme by sourcing its file")
	fmt.Fprintln(os.Stderr, "  save <name>   save current visual settings as a theme")
	fmt.Fprintln(os.Stderr, "  list          list available themes")
	fmt.Fprintln(os.Stderr, "  delete <name> remove a theme file")
	fmt.Fprintln(os.Stderr, "  help          show this help")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "themes are stored in ~/.config/lash/themes/")
}

func builtinThemeSet(args []string, ctx *ExecContext) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "lash: theme set: missing theme name")
		fmt.Fprintln(os.Stderr, "see 'lash theme help' for theme usage.")
		lastExitCode = 1
		return
	}
	name := args[0]
	if !isValidThemeName(name) {
		fmt.Fprintf(os.Stderr, "lash: theme set: '%s': invalid theme name\n", name)
		lastExitCode = 1
		return
	}
	dir := themesDirPath()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "lash: theme set: cannot determine themes directory")
		lastExitCode = 1
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "lash: theme set: '%s': theme not found\n", name)
		lastExitCode = 1
		return
	}
	if currentConfig != nil {
		currentConfig.Theme = name
		currentConfig.Save()
	}
	sourceFile(path, ctx.Cfg)
	lastExitCode = 0
}

func builtinThemeSave(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "lash: theme save: missing theme name")
		fmt.Fprintln(os.Stderr, "see 'lash theme help' for theme usage.")
		lastExitCode = 1
		return
	}
	name := args[0]
	if !isValidThemeName(name) {
		fmt.Fprintf(os.Stderr, "lash: theme save: '%s': invalid theme name\n", name)
		lastExitCode = 1
		return
	}
	dir := themesDirPath()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "lash: theme save: cannot determine themes directory")
		lastExitCode = 1
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "lash: theme save: %s\n", err)
		lastExitCode = 1
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		if !isTerminal() {
			fmt.Fprintf(os.Stderr, "lash: theme save: '%s' already exists (use interactive mode to overwrite)\n", name)
			lastExitCode = 1
			return
		}
		fmt.Fprintf(os.Stderr, "lash: theme save: '%s' already exists. Overwrite? [y/N] ", name)
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(os.Stderr, "aborted.")
			lastExitCode = 1
			return
		}
	}
	cfg := currentConfig
	if cfg == nil {
		cfg = LoadConfig()
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# lash theme: %s\n", name))
	b.WriteString("# visual\n")
	ps1 := getVar("PS1")
	if ps1 == "" {
		ps1 = defaultPS1
	}
	b.WriteString(fmt.Sprintf("export PS1='%s'\n", ps1))
	ps2 := getVar("PS2")
	if ps2 == "" {
		ps2 = "> "
	}
	b.WriteString(fmt.Sprintf("export PS2='%s'\n", ps2))
	ps3 := getVar("PS3")
	if ps3 != "" {
		b.WriteString(fmt.Sprintf("export PS3='%s'\n", ps3))
	}
	ps4 := getVar("PS4")
	if ps4 != "" {
		b.WriteString(fmt.Sprintf("export PS4='%s'\n", ps4))
	}
	b.WriteString("# config\n")
	b.WriteString(fmt.Sprintf("lash set-config syntax-color %s\n", boolToStr(cfg.SyntaxColor)))
	b.WriteString(fmt.Sprintf("lash set-config logosize %s\n", cfg.LogoSize))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "lash: theme save: %s\n", err)
		lastExitCode = 1
		return
	}
	fmt.Printf("lash: theme '%s' saved\n", name)
	lastExitCode = 0
}

func builtinThemeList() {
	dir := themesDirPath()
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "lash: theme list: %s\n", err)
			lastExitCode = 1
		}
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "lash: no themes found in ~/.config/lash/themes/")
		lastExitCode = 0
		return
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Println(n)
	}
	lastExitCode = 0
}

func builtinThemeDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "lash: theme delete: missing theme name")
		fmt.Fprintln(os.Stderr, "see 'lash theme help' for theme usage.")
		lastExitCode = 1
		return
	}
	name := args[0]
	if !isValidThemeName(name) {
		fmt.Fprintf(os.Stderr, "lash: theme delete: '%s': invalid theme name\n", name)
		lastExitCode = 1
		return
	}
	dir := themesDirPath()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "lash: theme delete: cannot determine themes directory")
		lastExitCode = 1
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "lash: theme delete: '%s': theme not found\n", name)
		lastExitCode = 1
		return
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "lash: theme delete: %s\n", err)
		lastExitCode = 1
		return
	}
	fmt.Printf("lash: theme '%s' deleted\n", name)
	if currentConfig != nil && currentConfig.Theme == name {
		currentConfig.Theme = ""
		currentConfig.Save()
	}
	lastExitCode = 0
}

func isValidThemeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	return true
}
