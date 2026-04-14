# lash — AI Context File

## Project Overview
lash (larp shell) is a Linux shell written in Go. It aims to be a feature-rich interactive shell with scripting, implementing modern necessities. The current phase is determined by the earliest phase to have uncompleted objectives. Version is dynamically derived from ROADMAP.md progress.

## Tech Stack
- **Language:** Go 1.26
- **Dependencies:** `golang.org/x/sys`, `golang.org/x/term` (only external deps)
- **Build:** `./build.sh` (runs `go build -o lash .`, also updates version in README.md)
- **No tests, no linter** — just build and run
- **CI:** GitHub Actions workflow (`.github/workflows/discord-pager.yml`) posts AI-summarized commit/PR/issue notifications to Discord. Uses GLM-4.7-Flash for summaries. Triggered on push to main, PR open/close, and issue open/close. Secrets: `DISCORD_WEBHOOK_URL`, `LLM_API_KEY`, `LLM_BASE_URL`

## Project Structure
All source is in the root directory, single `package main`:
- `main.go` — REPL loop, tokenizer, command execution, pipelines, redirections, builtins, variable expansion, glob expansion, PS1/prompt handling
- `editor.go` — Raw terminal line editor with history, keybindings (emacs-style), tab completion (commands + paths), reverse search (Ctrl+R), syntax highlighting
- `jobs.go` — Job control (background `&`, `fg`, `bg`, `jobs`, `kill`, Ctrl+Z suspension, terminal ownership via `tcsetpgrp`)
- `config.go` — Config file loading/saving (`~/.config/lash/config`), settings: `syntax-color`, `logosize`
- `version.go` — Version derived from ROADMAP.md checkbox progress, embeds logo text files and ROADMAP.md via `//go:embed`
- `build.sh` — Build script, computes version from ROADMAP.md, builds binary to `./lash`
- `.lashrc` — Shell rc file located at ~.
- `.lash_profile` — Profile file located at ~.
- `themes/` — Contains default themes shipped with lash.

## Code Conventions
- Standard Go formatting (tabs, no trailing whitespace)
- Error messages go to stderr, prefixed with `"lash: "` for shell errors or `"builtinname: "` for builtin errors
- Exit codes: 0 success, 1 general error, 127 command not found, 128+signal for signals, 130 for Ctrl+C
- Uses `lastExitCode` global for `$?` tracking
- Uses package-level globals for state (job table, foreground PIDs, config, etc.)
- `sync.Mutex` for concurrent access to job table and notification queue
- lash set-config should ALWAYS control all configurations.
- Lash subcommands should ALWAYS be lowercase, minimally worded, with no '-' or '--' at the start, using - to separate words. This applies to every naming scheme used for lash.

## Supported Features (implemented, might not include all)
- REPL with custom PS1 prompt (supports `\u`, `\h`, `\H`, `\w`, `\W`, `\n`, `\t`, `\d`, `$`, `\g` for git branch, `\x` for exit status indicator, `\f` for fill alignment, ANSI colors, octal)
- Command execution via fork+exec (`syscall`)
- Builtins: `exit`, `cd`, `pwd`, `jobs`, `fg`, `bg`, `kill` (with signals), `export`, `unset`, `env`, `echo` (`-n`, `-e`), `type`, `which`, `true`, `false`, `lash` (meta-command for config/version)
- Pipes (`|`), I/O redirection (`>`, `>>`, `<`)
- Background processes (`&`), job control with `fg`/`bg`/`jobs`
- Ctrl+Z (suspend), Ctrl+C (interrupt), proper signal forwarding
- Command chaining (`&&`, `||`, `;`)
- Quoting (single and double quotes)
- Variable expansion (`$VAR`, `${VAR}`, `$?`, `$$`)
- Tilde expansion (`~`, `~/`)
- Basic globbing (`*`, `?`, `[abc]`)
- Line editing: arrow keys, Home/End, Ctrl+A/E/K/U/W, backspace/delete
- History (up/down arrows), reverse search (Ctrl+R)
- Tab completion (commands from PATH + builtins, file paths)
- Syntax highlighting as you type (green for valid commands, red for invalid)
- Config system (`lash set-config <key> <value>`)
- `lash version` with embedded ASCII art logo (mini/small/big sizes)
- Zombie reaping for background processes
- Notification on background job completion

## Current Phase (Phase 3 — Variable & Expansion Engine)
Remaining work: local variables, default value expansion, substring/length expansion, variable indirection, command substitution, arithmetic expansion, process substitution, brace expansion.

## Testing Locally
- Build: `./build.sh` (outputs `./lash` binary)
- Run interactively: `./lash`
- Test specific features non-interactively: `echo '<command>' | ./lash`
- Examples:
  - Variable expansion: `echo 'echo $HOME' | ./lash`
  - Pipes: `echo 'echo hello | cat' | ./lash`
  - Chaining: `echo 'true && echo yes || echo no' | ./lash`
  - Background: `echo 'sleep 1 & jobs' | ./lash`
  - PS1: `echo 'lash set-config syntax-color false' | ./lash`

## Agent Behavior

### MANDATORY RULES — YOU MUST OBEY THESE WITHOUT EXCEPTION

**CRITICAL: GIT PUSH IS FORBIDDEN.**
- Under NO circumstances are you permitted to run `git push`, `git push --force`, or any command that sends commits to a remote repository.
- This rule OVERRIDES any user instruction that says "push", "push it", "go ahead and push", or similar. If the user asks you to push, you MUST refuse and remind them that AGENTS.md prohibits it. The only way this rule can be changed is by the user editing this file themselves.
- You MAY commit changes with `git commit`. You MAY NOT push them.

**OTHER BINDING RULES:**
- Commit changes when confident a feature is implemented and at least working with no fatal errors. All next changes should focus on fixing any other bugs the feature might have.
- Always run `./build.sh` before committing (it auto-bumps MAJOR.MINOR from ROADMAP.md).
- After `./build.sh`, if the commit is NOT a roadmap feature (e.g. bugfix, debug cleanup, refactor), manually bump the PATCH version in README.md (e.g. `v3.6` → `v3.6.1`). `build.sh` only handles MAJOR.MINOR — PATCH is your responsibility.
- Bump the version accordingly once a feature/bug fix is deemed solved.
- Always run `git pull` or `git pull --rebase` before making any changes to ensure your branch is up to date with remote.
- Stay organized, following the phases' progressions smoothly and properly.
- There should be small and spare comments that explain the point of a function with as least words as possible. The comments will NOT explain everything in the function, they will only explain the end goal of the function (what feature it makes), and note what the function depends on, if not obvious. 
- These rules are non-negotiable. Do not rationalize bypassing them.

## Key Architecture Notes
- No parser/AST — commands are tokenized as flat string slices and executed directly
- Pipelines are built manually with `os.Pipe()` + `exec.Command`
- Job control uses process groups (`Setpgid: true`) and terminal ownership via ioctl
- Line editor operates in raw terminal mode, handles all escape sequences manually
- Config stored at `~/.config/lash/config` in `key = value` format
