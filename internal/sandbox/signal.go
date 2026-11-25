package sandbox

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// maxSignalPasses bounds how often Signal rescans /proc for processes that
// appeared while it was signalling.
const maxSignalPasses = 5

// Signal sends sig to the process group led by pid (the run's bwrap, or
// the agent itself when unsandboxed) and to every process that may belong
// to the run although it left that group (setsid): pid's descendants, and
// every process of this uid in a PID namespace one of them is in, other
// than the caller's own. bwrap --unshare-pid puts the agent in its own PID
// namespace whose orphans are reparented to the namespace's init, so a
// process cannot leave both sets. The scan repeats until a pass finds no
// new process (at most maxSignalPasses passes): a process stopped by
// SIGSTOP cannot fork any more. It returns how many processes it signalled
// besides the group.
func Signal(pid int, sig syscall.Signal) (int, error) {
	if pid <= 0 {
		return 0, errors.New("sandbox: no process to signal")
	}
	groupErr := syscall.Kill(-pid, sig)
	if groupErr != nil {
		if err := syscall.Kill(pid, sig); err != nil {
			return 0, err
		}
	}
	own := nsLink(os.Getpid())
	uid := os.Getuid()
	done := map[int]bool{}
	for range maxSignalPasses {
		found := 0
		for _, p := range runProcs(pid, uid, own) {
			if done[p] {
				continue
			}
			done[p] = true
			found++
			_ = syscall.Kill(p, sig) // it may have exited meanwhile
		}
		if found == 0 {
			break
		}
	}
	return len(done), nil
}

type procInfo struct {
	ppid int
	ns   string
}

// runProcs lists root's descendants (root included) and the processes of
// uid sharing a PID namespace with one of them, other than own.
func runProcs(root, uid int, own string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	procs := map[int]procInfo{}
	children := map[int][]int{}
	for _, e := range entries {
		p, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fi, err := os.Stat("/proc/" + e.Name())
		if err != nil {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		ppid, ok := parentOf(p)
		if !ok {
			continue
		}
		procs[p] = procInfo{ppid: ppid, ns: nsLink(p)}
		children[ppid] = append(children[ppid], p)
	}
	set := map[int]bool{}
	ns := map[string]bool{}
	stack := []int{root}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if set[p] {
			continue
		}
		set[p] = true
		if info, ok := procs[p]; ok && info.ns != "" && info.ns != own {
			ns[info.ns] = true
		}
		stack = append(stack, children[p]...)
	}
	for p, info := range procs {
		if ns[info.ns] {
			set[p] = true
		}
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	return out
}

// parentOf reads a process's parent pid from /proc/<pid>/stat. The command
// name (field 2) may hold spaces and parentheses, so the fields are read
// after its last ')'.
func parentOf(pid int) (int, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	return ppid, err == nil
}

// nsLink is the PID namespace a process is in ("pid:[inode]"), or "".
func nsLink(pid int) string {
	l, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/pid")
	if err != nil {
		return ""
	}
	return l
}
