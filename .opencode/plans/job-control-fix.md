# Fix Job Control: Ctrl+Z and fg Hang

## Root Cause Analysis

### Bug 1: Current commit — Ctrl+Z has no effect
- Removed all process group management (`Setpgid: true`, `tcsetpgrp`, PGID tracking)
- Without process groups, shell and children share parent's PGID
- Without `tcsetpgrp`, terminal driver's process group signaling doesn't work for job control
- Go's `signal.Notify` catches SIGTSTP preventing default stop; `Wait4` + `WUNTRACED` interaction is unreliable without proper terminal control

### Bug 2: Previous commit — fg hang (task never ends, Ctrl+Z/C both broken)
- SIGCHLD handler goroutine races with `Wait4` in `waitForeground`
- `takeTerminal()` could fail after child exits; SIGTTIN then stops the shell when it tries to read
- `fg` handler sent SIGCONT BEFORE `waitForeground` gave terminal to child → child immediately gets SIGTTIN and re-stops

## Implementation Plan

### `jobs.go` changes:
1. Add `"unsafe"` to imports
2. Add `PGID int` to `Job` struct
3. Restore `tcsetpgrp()`, `giveTerminal()`, `takeTerminal()` functions
4. Restore `isForegroundPID()` for reapZombies protection
5. Update `initJobControl()`:
   - Restore `signal.Ignore(SIGTTOU)`, `Setpgid(0, 0)`, `tcsetpgrp` to take terminal
   - Keep signal forwarding goroutine for SIGINT; handle SIGTSTP via process groups
   - For SIGTSTP at prompt (no fg job): stop the shell
6. Update `addJob(pid, pgid, ...)` to accept pgid parameter
7. Update `waitForeground(pids, pgid, command)`:
   - Give terminal to child's PGID **before** sending SIGCONT
   - Send SIGCONT to all children after giving terminal (fixes SIGTTIN race)
   - Use `-pgid` for SIGTSTP in pipeline stop handling
   - `defer takeTerminal()` to always reclaim terminal
8. Keep `setFgPIDs` / `clearFgPIDs` functions (from current version's naming)

### `main.go` changes:
1. Restore `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` in `executeSimple` and `executePipeline`
2. Update `reapZombies` to skip foreground PIDs via `isForegroundPID`
3. Remove `takeTerminal()` call from main loop (handled by `waitForeground`'s defer)
4. Update `fg` handler:
   - Remove SIGCONT (handled inside `waitForeground` after giveTerminal)
   - Pass `job.PGID` to `waitForeground`
5. Update `bg` handler: send SIGCONT to process group (`-job.PGID`)
6. Update `kill` handler: send signals to process group (`-job.PGID`)
7. Update `executeSimple`: pass `cmd.Process.Pid` as pgid to `addJob` and `waitForeground`
8. Update `executePipeline`: pass `pids[0]` as pgid to `addJob` and `waitForeground`

## Key Design Decisions
- **No SIGCHLD handler goroutine** — use polling-only approach (current version's reapZombies in main loop) to eliminate races with Wait4
- **SIGCONT inside waitForeground** — after giveTerminal, so child has terminal access when resumed
- **Signal forwarding for SIGINT only** — SIGTSTP is handled via process groups (terminal delivers directly to child's PGID)
