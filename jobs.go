package main

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	trapTable   map[syscall.Signal]string
	trapMu      sync.RWMutex
	trapRunning bool
)

func initTrapTable() {
	trapTable = make(map[syscall.Signal]string)
}

func getTrap(sig syscall.Signal) string {
	trapMu.RLock()
	defer trapMu.RUnlock()
	return trapTable[sig]
}

func setTrap(sig syscall.Signal, handler string) {
	trapMu.Lock()
	defer trapMu.Unlock()
	trapTable[sig] = handler
}

func clearTrap(sig syscall.Signal) {
	trapMu.Lock()
	defer trapMu.Unlock()
	delete(trapTable, sig)
}

func runTrapHandler(sig syscall.Signal) {
	trapMu.RLock()
	handler := trapTable[sig]
	trapMu.RUnlock()
	if handler == "" {
		return
	}
	if handler == "-" {
		clearTrap(sig)
		return
	}
	if trapRunning {
		return
	}
	trapRunning = true
	defer func() { trapRunning = false }()

	lastExitCode = 128 + int(sig)
	expanded := expandString(handler)
	prog := Parse(expanded)
	executeNode(prog, defaultContext())
}

type JobState int

const (
	JobRunning JobState = iota
	JobStopped
	JobDone
)

type Job struct {
	Number   int
	PID      int
	PGID     int
	State    JobState
	Command  string
	ExitCode int
}

var (
	jobTable   []*Job
	nextJobNum int
	jobMu      sync.Mutex

	fgPIDs   map[int]bool
	fgActive bool
	fgMu     sync.Mutex
)

func initJobControl() {
	fgPIDs = make(map[int]bool)

	signal.Ignore(syscall.SIGTTOU)

	syscall.Setpgid(0, 0)
	tcsetpgrp(int(os.Stdin.Fd()), syscall.Getpgrp())

	sigCh := make(chan os.Signal, 32)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTSTP, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGALRM, syscall.SIGPIPE, syscall.SIGWINCH)
	go func() {
		for sig := range sigCh {
			s := sig.(syscall.Signal)
			if s == syscall.SIGKILL || s == syscall.SIGSTOP {
				continue
			}
			fgMu.Lock()
			active := fgActive
			pids := make([]int, 0, len(fgPIDs))
			for p := range fgPIDs {
				pids = append(pids, p)
			}
			fgMu.Unlock()
			if active && len(pids) > 0 {
				for _, p := range pids {
					syscall.Kill(p, s)
				}
				continue
			}
			trapMu.RLock()
			handler, trapped := trapTable[s]
			trapMu.RUnlock()
			if trapped {
				if handler == "" {
					continue
				}
				runTrapHandler(s)
				continue
			}
			if s == syscall.SIGTSTP {
				signal.Stop(sigCh)
				syscall.Kill(syscall.Getpid(), syscall.SIGTSTP)
				signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTSTP, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGALRM, syscall.SIGPIPE, syscall.SIGWINCH)
			}
		}
	}()
}

func addJob(pid, pgid int, state JobState, command string) *Job {
	jobMu.Lock()
	defer jobMu.Unlock()

	cleanupDoneJobsLocked()

	usedNums := make(map[int]bool)
	for _, j := range jobTable {
		usedNums[j.Number] = true
	}
	for {
		nextJobNum++
		if !usedNums[nextJobNum] {
			break
		}
	}

	job := &Job{
		Number:  nextJobNum,
		PID:     pid,
		PGID:    pgid,
		State:   state,
		Command: command,
	}
	jobTable = append(jobTable, job)
	return job
}

func findJobByNumber(num int) *Job {
	jobMu.Lock()
	defer jobMu.Unlock()
	for _, j := range jobTable {
		if j.Number == num && j.State != JobDone {
			return j
		}
	}
	return nil
}

func findJobByPID(pid int) *Job {
	jobMu.Lock()
	defer jobMu.Unlock()
	for _, j := range jobTable {
		if j.PID == pid && j.State != JobDone {
			return j
		}
	}
	return nil
}

func markJobDone(pid int, exitCode int) {
	jobMu.Lock()
	defer jobMu.Unlock()
	for _, j := range jobTable {
		if j.PID == pid {
			j.State = JobDone
			j.ExitCode = exitCode
			break
		}
	}
}

func markJobStopped(pid int) {
	jobMu.Lock()
	defer jobMu.Unlock()
	for _, j := range jobTable {
		if j.PID == pid {
			j.State = JobStopped
			break
		}
	}
}

func markJobRunningByPID(pid int) {
	jobMu.Lock()
	defer jobMu.Unlock()
	for _, j := range jobTable {
		if j.PID == pid {
			j.State = JobRunning
			break
		}
	}
}

func getMostRecentJob() *Job {
	jobMu.Lock()
	defer jobMu.Unlock()
	for i := len(jobTable) - 1; i >= 0; i-- {
		if jobTable[i].State == JobStopped {
			return jobTable[i]
		}
	}
	for i := len(jobTable) - 1; i >= 0; i-- {
		if jobTable[i].State == JobRunning {
			return jobTable[i]
		}
	}
	return nil
}

func parseJobSpec(spec string) (*Job, error) {
	if len(spec) >= 2 && spec[0] == '%' {
		num, err := strconv.Atoi(spec[1:])
		if err != nil {
			return nil, fmt.Errorf("%s: no such job", spec)
		}
		job := findJobByNumber(num)
		if job == nil {
			return nil, fmt.Errorf("%s: no such job", spec)
		}
		return job, nil
	}
	pid, err := strconv.Atoi(spec)
	if err != nil {
		return nil, fmt.Errorf("%s: argument must be %%job or pid", spec)
	}
	job := findJobByPID(pid)
	if job == nil {
		return nil, fmt.Errorf("%s: no such job", spec)
	}
	return job, nil
}

func printJobs() {
	jobMu.Lock()
	defer jobMu.Unlock()

	var active []*Job
	for _, j := range jobTable {
		if j.State != JobDone {
			active = append(active, j)
		}
	}

	if len(active) == 0 {
		return
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].Number < active[j].Number
	})

	for i, j := range active {
		marker := " "
		if i == len(active)-1 {
			marker = "+"
		} else if i == len(active)-2 {
			marker = "-"
		}

		stateStr := "Running"
		if j.State == JobStopped {
			stateStr = "Stopped"
		}

		fmt.Printf("[%d]%s  %-9s %s\n", j.Number, marker, stateStr, j.Command)
	}
}

func cleanupDoneJobsLocked() {
	var alive []*Job
	for _, j := range jobTable {
		if j.State != JobDone {
			alive = append(alive, j)
		}
	}
	jobTable = alive
}

func setFgPIDs(pids []int) {
	fgMu.Lock()
	defer fgMu.Unlock()
	fgPIDs = make(map[int]bool)
	for _, p := range pids {
		fgPIDs[p] = true
	}
	fgActive = true
}

func clearFgPIDs() {
	fgMu.Lock()
	defer fgMu.Unlock()
	fgPIDs = make(map[int]bool)
	fgActive = false
}

func isForegroundPID(pid int) bool {
	fgMu.Lock()
	defer fgMu.Unlock()
	return fgPIDs[pid]
}

func handleChildReap(pid int, status syscall.WaitStatus) {
	job := findJobByPID(pid)
	if job == nil {
		return
	}

	if status.Stopped() {
		markJobStopped(pid)
		notifMu.Lock()
		pendingNotifs = append(pendingNotifs, fmt.Sprintf("\n[%d]+  Stopped    %s\n", job.Number, job.Command))
		notifMu.Unlock()
	} else {
		exitCode := 0
		if status.Exited() {
			exitCode = status.ExitStatus()
		} else if status.Signaled() {
			exitCode = 128 + int(status.Signal())
		}
		markJobDone(pid, exitCode)
		notifMu.Lock()
		pendingNotifs = append(pendingNotifs, fmt.Sprintf("[%d]+  Done       %s\n", job.Number, job.Command))
		notifMu.Unlock()
	}
}

func tcsetpgrp(fd int, pgid int) error {
	var p int32 = int32(pgid)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&p)))
	if errno != 0 {
		return errno
	}
	return nil
}

func giveTerminal(pgid int) {
	_ = tcsetpgrp(int(os.Stdin.Fd()), pgid)
}

func takeTerminal() {
	_ = tcsetpgrp(int(os.Stdin.Fd()), syscall.Getpgrp())
}

func waitForeground(pids []int, pgid int, command string) int {
	codes := waitForegroundCodes(pids, pgid, command)
	if len(codes) == 0 {
		return 0
	}
	return codes[len(codes)-1]
}

func waitForegroundCodes(pids []int, pgid int, command string) []int {
	setFgPIDs(pids)
	defer clearFgPIDs()

	remaining := make(map[int]bool)
	for _, p := range pids {
		remaining[p] = true
	}

	for _, p := range pids {
		syscall.Setpgid(p, pgid)
	}
	syscall.Setpgid(0, 0)
	giveTerminal(pgid)
	defer takeTerminal()

	for _, p := range pids {
		syscall.Kill(p, syscall.SIGCONT)
	}

	codes := make([]int, 0, len(pids))
	for len(remaining) > 0 {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WUNTRACED, nil)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			break
		}
		if pid <= 0 {
			break
		}

		if !remaining[pid] {
			handleChildReap(pid, status)
			continue
		}

		if status.Stopped() {
			syscall.Kill(-pgid, syscall.SIGTSTP)
			for otherPid := range remaining {
				if otherPid == pid {
					delete(remaining, otherPid)
					continue
				}
				var s2 syscall.WaitStatus
				for {
					wp, we := syscall.Wait4(otherPid, &s2, syscall.WUNTRACED, nil)
					if we != nil {
						if we == syscall.EINTR {
							continue
						}
						break
					}
					if wp == otherPid {
						break
					}
					break
				}
				delete(remaining, otherPid)
			}
			job := addJob(pids[0], pgid, JobStopped, command)
			fmt.Printf("\n[%d]+  Stopped    %s\n", job.Number, job.Command)
			return append(codes, -1)
		}

		if status.Exited() {
			codes = append(codes, status.ExitStatus())
		} else if status.Signaled() {
			codes = append(codes, 128+int(status.Signal()))
		}
		delete(remaining, pid)
	}

	return codes
}

func listSignals() []syscall.Signal {
	return []syscall.Signal{
		syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT,
		syscall.SIGILL, syscall.SIGTRAP, syscall.SIGABRT,
		syscall.SIGBUS, syscall.SIGFPE, syscall.SIGKILL,
		syscall.SIGUSR1, syscall.SIGSEGV, syscall.SIGUSR2,
		syscall.SIGPIPE, syscall.SIGALRM, syscall.SIGTERM,
		syscall.SIGSTKFLT, syscall.SIGCHLD, syscall.SIGCONT,
		syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN,
		syscall.SIGTTOU, syscall.SIGURG, syscall.SIGXCPU,
		syscall.SIGXFSZ, syscall.SIGVTALRM, syscall.SIGPROF,
		syscall.SIGWINCH, syscall.SIGIO, syscall.SIGPWR,
		syscall.SIGSYS,
	}
}

func parseSignal(s string) (syscall.Signal, error) {
	n, err := strconv.Atoi(s)
	if err == nil {
		return syscall.Signal(n), err
	}
	sigNames := map[string]syscall.Signal{
		"HUP": syscall.SIGHUP, "INT": syscall.SIGINT, "QUIT": syscall.SIGQUIT,
		"ILL": syscall.SIGILL, "TRAP": syscall.SIGTRAP, "ABRT": syscall.SIGABRT,
		"BUS": syscall.SIGBUS, "FPE": syscall.SIGFPE, "KILL": syscall.SIGKILL,
		"USR1": syscall.SIGUSR1, "SEGV": syscall.SIGSEGV, "USR2": syscall.SIGUSR2,
		"PIPE": syscall.SIGPIPE, "ALRM": syscall.SIGALRM, "TERM": syscall.SIGTERM,
		"STKFLT": syscall.SIGSTKFLT, "CHLD": syscall.SIGCHLD, "CONT": syscall.SIGCONT,
		"STOP": syscall.SIGSTOP, "TSTP": syscall.SIGTSTP, "TTIN": syscall.SIGTTIN,
		"TTOU": syscall.SIGTTOU, "URG": syscall.SIGURG, "XCPU": syscall.SIGXCPU,
		"XFSZ": syscall.SIGXFSZ, "VTALRM": syscall.SIGVTALRM, "PROF": syscall.SIGPROF,
		"WINCH": syscall.SIGWINCH, "IO": syscall.SIGIO, "PWR": syscall.SIGPWR,
		"SYS": syscall.SIGSYS,
	}
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "SIG") {
		upper = upper[3:]
	}
	if sig, ok := sigNames[upper]; ok {
		return sig, nil
	}
	return 0, fmt.Errorf("invalid signal: %s", s)
}

func signalName(sig syscall.Signal) string {
	names := map[syscall.Signal]string{
		syscall.SIGHUP: "HUP", syscall.SIGINT: "INT", syscall.SIGQUIT: "QUIT",
		syscall.SIGILL: "ILL", syscall.SIGTRAP: "TRAP", syscall.SIGABRT: "ABRT",
		syscall.SIGBUS: "BUS", syscall.SIGFPE: "FPE", syscall.SIGKILL: "KILL",
		syscall.SIGUSR1: "USR1", syscall.SIGSEGV: "SEGV", syscall.SIGUSR2: "USR2",
		syscall.SIGPIPE: "PIPE", syscall.SIGALRM: "ALRM", syscall.SIGTERM: "TERM",
		syscall.SIGSTKFLT: "STKFLT", syscall.SIGCHLD: "CHLD", syscall.SIGCONT: "CONT",
		syscall.SIGSTOP: "STOP", syscall.SIGTSTP: "TSTP", syscall.SIGTTIN: "TTIN",
		syscall.SIGTTOU: "TTOU", syscall.SIGURG: "URG", syscall.SIGXCPU: "XCPU",
		syscall.SIGXFSZ: "XFSZ", syscall.SIGVTALRM: "VTALRM", syscall.SIGPROF: "PROF",
		syscall.SIGWINCH: "WINCH", syscall.SIGIO: "IO", syscall.SIGPWR: "PWR",
		syscall.SIGSYS: "SYS",
	}
	if name, ok := names[sig]; ok {
		return name
	}
	return sig.String()
}
