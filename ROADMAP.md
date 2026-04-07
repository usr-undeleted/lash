# lash — Roadmap

## Phase 1: Core Foundation

- [x] REPL loop with dynamic prompt
- [x] Basic command execution (fork+exec)
- [x] Built-in commands (exit, cd, pwd)
- [x] Pipes (`|`)
- [x] I/O redirection (`>`, `>>`, `<`)
- [x] Background processes (`&`)
- [x] Basic Ctrl+C handling
- [x] Quoting (single and double quotes)
- [x] Command chaining (`&&`, `||`, `;`)
- [x] Exit status codes (`$?`)
- [x] Signal handling for background jobs (`SIGCHLD`)
- [x] Proper zombie reaping for background processes

## Phase 2: Essential Interactive Features

- [x] Command history (up/down arrows)
- [x] Line editing (left/right arrows, backspace, delete, Home/End)
- [x] Tab completion (commands, file paths)
- [x] Reverse search (Ctrl+R)
- [x] Ctrl+L to clear screen
- [x] Ctrl+D to exit on empty line
- [x] Ctrl+A / Ctrl+E (beginning/end of line)
- [x] Ctrl+K / Ctrl+U (kill to end/beginning of line)
- [x] Ctrl+W (delete word backwards)
- [x] Job control: `jobs`, `fg`, `bg`, `kill` builtins
- [x] Suspended processes (Ctrl+Z)
- [x] More built-ins: `export`, `unset`, `env`, `set`, `echo` with flags (`-n`, `-e`), `type`, `which`, `true`, `false`, `test`/`[`
- [x] Basic wildcard/glob expansion (`*`, `?`)
## Phase 3: Variable & Expansion Engine

- [x] Local variables (`name=value`)
- [x] Environment variables (`export name=value`)
- [x] Variable expansion (`$VAR`, `${VAR}`)
- [x] Default value expansion (`${VAR:-default}`, `${VAR:=default}`, `${VAR:+alt}`, `${VAR:?err}`)
- [x] Substring expansion (`${VAR:offset:length}`)
- [x] Length expansion (`${#VAR}`)
- [x] Variable indirection (`${!ref}`)
- [x] Tilde expansion (`~`, `~/`, `~user/`)
- [x] Command substitution (`$(cmd)`, backtick `` `cmd` ``)
- [x] Arithmetic expansion (`$((expr))`)
- [x] Process substitution (`<(cmd)`, `>(cmd)`)
- [x] Brace expansion (`{a,b,c}`, `{1..10}`)

## Phase 4: Pattern Matching & Globbing

- [x] Basic globbing (`*`, `?`, `[abc]`, `[!abc]`)
- [x] Recursive globbing (`**`)
- [x] Extended globbing (`?(pattern)`, `*(pattern)`, `+(pattern)`, `@(pattern)`, `!(pattern)`)
- [x] Globbing with dotfiles option
- [x] Case-sensitive/insensitive matching option
- [x] Filename quoting and dequoting

## Phase 5: Scripting & Control Flow (current)

### Phase 4→5 Refactor (prerequisite)
- [x] Split `main.go` — extract `tokenizer.go`, `builtins.go`, `executor.go`; keep REPL loop in main.go
- [x] Implement minimal AST (`ast.go`) with node types: `Command`, `Pipeline`, `IfStatement`, `WhileStatement`, `ForStatement`, `CaseStatement`
- [x] Replace flat token-slice execution with AST-based execution for new control flow constructs

- [x] `if` / `then` / `elif` / `else` / `fi`
- [x] `while` / `do` / `done`
- [x] `until` / `do` / `done`
- [x] `for` / `in` / `do` / `done`
- [x] C-style `for` loops (`for ((i=0; i<10; i++))`)
- [x] `case` / `esac`
- [x] `select` / `do` / `done`
- [x] Functions (`func() { ... }`)
- [x] Function arguments and local variables (`local`)
- [x] Return values from functions (`return`)
- [x] Positional parameters (`$0`, `$1`..`$9`, `$@`, `$*`, `$#`)
- [x] `shift` builtin
- [x] `read` builtin (with flags for prompts, delimiters, etc.)
- [x] Here documents (`<< EOF`)
- [x] Here strings (`<<< "string"`)
- [x] `set` / `unset` with various flags (`set -e`, `set -x`, `set -o pipefail`)
- [ ] Trap command (`trap 'handler' SIGNAL`)
- [x] `break`, `continue`, `exit` in loops
- [ ] Conditional expressions in `[[ ]]` and `[ ]`
- [ ] String comparisons and pattern matching in conditionals
- [ ] Array support (indexed: `arr=(a b c)`, associative: `declare -A map`)
- [ ] Array slicing and iteration
- [x] Subshell execution (`(cmd)`)
- [x] Command grouping (`{ cmd1; cmd2; }`)

## Phase 6: Configuration & Customization

- [x] Config file loading (`~/.lashrc`, `~/.config/lash/config`)
- [x] Prompt customization with escape sequences (`\u`, `\h`, `\w`, `\n`, colors)
- [ ] Left and right prompts (PS1/PS2/RPS1)
- [ ] Prompt themes (predefined themes, user-switchable)
- [x] Aliases (`alias ll='ls -la'`)
- [ ] Key binding configuration
- [ ] Shell options system (`setopt`, `shopt` style)
- [ ] Login shell vs non-login shell vs interactive detection
- [ ] Profile files (`~/.lash_profile`, `/etc/lash_profile`)
- [ ] `PATH` management helpers
- [ ] Colored output support (LS_COLORS, GREP_COLORS integration)
- [ ] Per-directory local config (`.lashenv`)

## Phase 7: Advanced Interactive Features

- [ ] Multi-line command editing (with visual indicator)
- [ ] Auto-suggestions (fish-style灰色 hints)
- [x] Syntax highlighting as you type
- [ ] Tab completion menus (when ambiguous)
- [ ] Completion descriptions (showing what each option does)
- [ ] Command-not-found hook (suggest packages to install)
- [ ] Auto-correct / fuzzy matching for commands and directories (correct typos like "prkjects" → "projects" using Levenshtein distance, with confidence threshold and `setopt autocorrect` toggle)
- [ ] Directory history (`cd -<number>`, `cdh`)
- [ ] Auto-cd (type directory name to cd into it)
- [ ] Smart cd with frecency/parent matching (`z`-style)
- [ ] Clipboard integration
- [ ] URL detection and click-to-open
- [ ] Thumbnail previews in completion (for images/icons)
- [ ] Right-aligned prompt info (git branch, timer, etc.)
- [ ] Command duration tracking (show time for slow commands)
- [ ] Notification on long-running command completion

## Phase 8: Performance & Architecture

- [ ] Startup time benchmarking and optimization (target: <100ms)
- [ ] Lazy-loaded builtins
- [ ] Caching for completions and path lookups
- [ ] Minimal memory footprint
- [ ] Parallel pipeline execution optimization
- [ ] Hash table for command path caching (`hash` builtin)
- [ ] Efficient string handling and memory management

## Phase 9: Portability & Compatibility

- [ ] Linux (x86_64, ARM64) — primary target
- [ ] macOS support
- [ ] WSL support
- [ ] POSIX sh compatibility mode
- [ ] Bash compatibility mode (emulate bash behavior)
- [ ] Zsh compatibility features
- [ ] Cross-compile support (GOOS/GOARCH)

## Phase 10: Documentation & Community

- [ ] Man page (`man lash`)
- [ ] Built-in `help` command for each builtin
- [ ] Full online documentation / website
- [ ] Tutorial / getting started guide
- [ ] Scripting guide (migrating from bash)
- [ ] Configuration examples and themes
- [ ] Contribution guide
- [ ] Plugin API documentation
- [ ] Arch Linux AUR package
- [ ] Nix flake
- [ ] Homebrew formula

## Phase 11: Ecosystem & Extensibility

- [ ] Plugin system (loadable Go plugins or scripts)
- [ ] Package manager for shell extensions
- [ ] Community theme repository
- [ ] Completion definition framework (like zsh's compsys)
- [ ] Custom widget functions for key bindings
- [ ] Hook system (precmd, preexec, chpwd, periodic)
- [ ] Virtualenv/conda integration helpers
- [ ] SSH agent / GPG agent integration
- [ ] Starship/pure/powerlevel10k-like prompt integration support

## Phase 12: Polish & "Nice to Have"

- [ ] Shell script linter / syntax checker (lash -n script.lash)
- [ ] Shell script profiler (lash -x script.lash)
- [ ] Interactive debugger for scripts (breakpoints, step, inspect variables)
- [ ] Built-in `man` / `help` with examples
- [ ] Spell-checking for commands (did you mean?)
- [ ] Directory stack (`pushd`, `popd`, `dirs`)
- [ ] Shared command history across sessions
- [x] Session restore (reopen shell with history)
- [ ] Terminal title setting
- [ ] 256-color and truecolor support in prompts
- [ ] Unicode / emoji aware string handling
- [ ] Right-to-left language support consideration
- [ ] Accessibility features
