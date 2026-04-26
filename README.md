# lash

<p align="center">
  <img src="logo/lash.png" alt="lash" width="325">
</p>

<p align="center">
  <img src="https://img.shields.io/badge/version-v7.6.8-blue">
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go">
  <img src="https://img.shields.io/badge/license-GPLv3-blue">
</p>

<p align="center">
  <img src="logo/demo.gif" alt="lash demo" width="600">
</p>

A modern Linux shell written in Go — with fuzzy autocorrect, fish-style completion, a built-in updater, and rich prompt customization.

## installation

```bash
chmod +x setup.sh
./setup.sh
```

The setup wizard builds lash, lets you pick an install location (`/usr/local/bin` or `~/.local/bin`), copies themes, and walks you through configuration — press Enter through every prompt to enable all features.

## quick start

```bash
lash              # start an interactive session
lash help         # list all lash commands
lash doctor       # diagnose common issues
```

## key features

**Auto-correct** — mistyped commands are fixed on the fly using Damerau-Levenshtein fuzzy matching.
```bash
lash set-config autocorrect 1
ehco hello          # lash: corrected 'ehco' to 'echo' → hello
```

**Auto-cd** — type a directory name to change into it.
```bash
lash set-config auto-cd 1
projects/           # chdir into projects/
```

**Fish-style tab cycling** — press Tab to cycle through completions in-place. No grid dump.

**Fuzzy tab completion** — typos show distance-sorted candidates on Tab.
```bash
prpjec<Tab>         # cycles through projects/, pkexec, prove...
```

**Directories-only cd completion** — `cd <Tab>` shows only directories, never files.

**Completion menus** with built-in descriptions for commands and flags.

**Syntax highlighting** — valid commands turn green, invalid go red, keywords appear yellow as you type.

**Tab completion menus** — a navigable menu appears for ambiguous completions.

**Auto-suggestions** — fish-style grayed-out inline hints as you type.

## configuration

All settings live under a single interface:
```bash
lash set-config list              # list all settings
lash set-config show              # show current values
lash set-config autocorrect 1     # enable autocorrect
lash set-config auto-cd 1         # enable auto-cd
lash set-config autocorrect-threshold 4  # adjust fuzzy distance (1-4)
```

Config file: `~/.config/lash/config`

Themes:
```bash
lash theme list                   # list available themes
lash theme set slim               # switch to slim prompt
```

Key bindings:
```bash
lash keybind list                 # list all keybinds
lash keybind set <key> <action>   # customize a binding
```

Per-directory config:
```bash
lash env allow                    # trust .lashenv in this directory
```

## updating

```bash
lash update                       # git pull + build + install
```

Update notifications appear on startup (once per day) when new commits are available on the upstream branch.

## ps1

Rich prompt with escape sequences:

| Sequence | Meaning |
|----------|---------|
| `\u` | username |
| `\h` | short hostname |
| `\H` | full hostname |
| `\w` | working directory |
| `\W` | basename of working directory |
| `\n` | newline |
| `\t` | time (HH:MM:SS) |
| `\d` | date |
| `\g` | git branch |
| `\x` | last exit status indicator |
| `\f` | fill line (flexible spacer) |
| `\F` | fill line with a custom character |

ANSI color codes and octal escapes are supported anywhere in PS1.

<p align="center">
  <img src="logo/examples.png" alt="lash prompt examples" width="600">
</p>

## roadmap

See [ROADMAP.md](ROADMAP.md)

## contributors

<p align="left">
  <a href="https://github.com/usr-undeleted/lash/graphs/contributors">
    <img src="https://contrib.rocks/image?repo=usr-undeleted/lash" />
  </a>
</p>
