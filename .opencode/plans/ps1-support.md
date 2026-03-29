# Add PS1 Environment Variable Support

## Summary
Make the prompt use `$PS1` if set, with full bash-like escape expansion. Default PS1 matches the current prompt appearance.

## Changes

### `main.go` — `getPrompt()` and new `expandPS1()`

**Modify `getPrompt()`** (line 69):
- Check `os.Getenv("PS1")`; if empty, use `defaultPS1` constant
- Call `expandPS1()` on the PS1 string and return result
- Remove current hardcoded prompt logic

**Add `expandPS1(ps1 string) string`** function:
Expand PS1 escape sequences:
| Escape | Expansion |
|--------|-----------|
| `\u` | username |
| `\h` | short hostname (up to first `.`) |
| `\H` | full hostname |
| `\w` | cwd with `~` substitution |
| `\W` | basename of cwd |
| `\n` | newline |
| `\$` | `#` if root, `$` otherwise |
| `\\` | literal backslash |
| `\t` | time HH:MM:SS |
| `\d` | date "Weekday Month Date" |
| `\!` | command number (incrementing counter) |
| `\g` | git branch (empty if none) |
| `\x` | `✗` in red if lastExitCode != 0, empty otherwise |
| `\[` | non-printing begin delimiter → stripped |
| `\]` | non-printing end delimiter → stripped |
| `\e` | `\x1b` escape character |
| `\a` | bell (`\x07`) |
| `\033` | `\x1b` (3-digit octal) |

**Add `defaultPS1` constant**:
```
\[\e[1;36m\]\u@\h\[\e[0m\] \[\e[1;33m\]\w\[\e[0m\] on \[\e[1m\]\g\[\e[0m\]\x\r\n\[\e[1m\]╰\$\[\e[0m\] 
```
This matches the current prompt exactly (bold cyan user@host, bold yellow cwd, bold git branch, ✗ on failure, bold ╰$).

| `\f` | right-fill: pads with spaces to push everything after it to the right edge of the terminal |

**`\f` implementation (right-fill)**:
When `\f` is encountered in the PS1 string:
1. Split the PS1 into left-half and right-half at `\f`
2. Expand both halves independently (recursively or via a helper)
3. Calculate `visibleWidth(leftExpanded)` and `visibleWidth(rightExpanded)` using the existing `visibleWidth()` from `editor.go`
4. Get terminal width via `getTermWidth()` (already exists in `editor.go`)
5. Insert `max(0, termWidth - leftWidth - rightWidth)` spaces between them
6. Example: `PS1='\u@\h \w\f \t\n\$ '` with 80-col terminal renders:
   ```
   user@host ~/project                     14:30:25
   ╰$ 
   ```
   If terminal is 40 cols and content is 35 chars total, inserts 5 spaces.

**Add `cmdNumber` counter variable**: incremented each time a command is executed (for `\!`).

### No changes to `editor.go`
`visibleWidth()` and `getTermWidth()` already exist and handle ANSI escapes correctly — PS1 expansion produces real `\x1b` sequences that the existing width calculation skips.
