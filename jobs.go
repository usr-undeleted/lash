package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

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
	fgPIDs     map[int]bool
	fgMu       sync.Mutex
	waitingFG  bool
)

func initJobControl() {
	fgPIDs = make(map[int]bool)
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

func setForegroundPIDs(pids []int) {
	fgMu.Lock()
	defer fgMu.Unlock()
	fgPIDs = make(map[int]bool)
	for _, p := range pids {
		fgPIDs[p] = true
	}
	waitingFG = true
}

func clearForegroundPIDs() {
	fgMu.Lock()
	defer fgMu.Unlock()
	fgPIDs = make(map[int]bool)
	waitingFG = false
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
	var p [4]byte
	*(*int32)(unsafe.Pointer(&p[0])) = int32(pgid)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&p[0])))
	if errno != 0 {
		return errno
	}
	return nil
}

func giveTerminal(pgid int) {
	tcsetpgrp(0, pgid)
}

func takeTerminal() {
	tcsetpgrp(0, syscall.Getpgrp())
}

func waitForeground(pids []int, pgid int, command string) int {
	setForegroundPIDs(pids)
	defer clearForegroundPIDs()

	remaining := make(map[int]bool)
	for _, p := range pids {
		remaining[p] = true
	}

	giveTerminal(pgid)
	defer takeTerminal()

	lastExit := 0
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
			return -1
		}

		if status.Exited() {
			lastExit = status.ExitStatus()
		} else if status.Signaled() {
			lastExit = 128 + int(status.Signal())
		}
		delete(remaining, pid)
	}

	return lastExit
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
		return syscall.Signal(n), nil
	}
	name := strings.ToUpper(s)
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	for _, sig := range listSignals() {
		if sig.String() == name {
			return sig, nil
		}
	}
	return 0, fmt.Errorf("invalid signal: %s", s)
}
