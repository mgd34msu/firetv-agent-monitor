package process

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

// On macOS one sysctl returns the name and start time of every process. It
// is about a hundred times cheaper than querying each process in turn, which
// matters for a loop that lists processes every second all day.

func startTime(_ context.Context, pid int) (time.Time, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, false // no such process
	}
	return time.Unix(k.Proc.P_starttime.Unix()), true
}

func findByName(ctx context.Context, name string) ([]Info, error) {
	// p_comm keeps the first 16 bytes (MAXCOMLEN) of the executable name,
	// enough for the names the detectors look for.
	return list(ctx, func(k *unix.KinfoProc) bool { return unix.ByteSliceToString(k.Proc.P_comm[:]) == name })
}

func findByNamePrefix(ctx context.Context, prefix string) ([]Info, error) {
	return list(ctx, func(k *unix.KinfoProc) bool {
		return strings.HasPrefix(unix.ByteSliceToString(k.Proc.P_comm[:]), prefix)
	})
}

func children(ctx context.Context, parents []int) ([]Info, error) {
	return list(ctx, func(k *unix.KinfoProc) bool { return slices.Contains(parents, int(k.Eproc.Ppid)) })
}

// list describes the processes that match, reading the working directory and
// command line of those only.
func list(ctx context.Context, match func(*unix.KinfoProc) bool) ([]Info, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	var found []Info
	for i := range all {
		k := &all[i]
		if !match(k) {
			continue
		}
		pid := int(k.Proc.P_pid)
		dir, args := inspect(ctx, pid)
		found = append(found, Info{
			PID:       pid,
			Name:      unix.ByteSliceToString(k.Proc.P_comm[:]),
			Dir:       dir,
			Args:      args,
			StartedAt: time.Unix(k.Proc.P_starttime.Unix()),
		})
	}
	return found, nil
}

// inspect returns the working directory and command line of a process, each
// empty if it is not readable.
func inspect(ctx context.Context, pid int) (string, []string) {
	p, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return "", nil
	}
	dir, _ := p.CwdWithContext(ctx)
	args, _ := p.CmdlineSliceWithContext(ctx)
	return dir, args
}
