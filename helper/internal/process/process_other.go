//go:build !darwin

package process

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func startTime(ctx context.Context, pid int) (time.Time, bool) {
	p, err := process.NewProcessWithContext(ctx, int32(pid))
	if err != nil {
		return time.Time{}, false
	}
	millis, err := p.CreateTimeWithContext(ctx)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(millis), true
}

func findByName(ctx context.Context, name string) ([]Info, error) {
	return list(ctx, func(p *process.Process) bool {
		pname, err := p.NameWithContext(ctx)
		return err == nil && pname == name
	})
}

func findByNamePrefix(ctx context.Context, prefix string) ([]Info, error) {
	return list(ctx, func(p *process.Process) bool {
		pname, err := p.NameWithContext(ctx)
		return err == nil && strings.HasPrefix(pname, prefix)
	})
}

func children(ctx context.Context, parents []int) ([]Info, error) {
	return list(ctx, func(p *process.Process) bool {
		ppid, err := p.PpidWithContext(ctx)
		return err == nil && slices.Contains(parents, int(ppid))
	})
}

// list describes the processes that match.
func list(ctx context.Context, match func(*process.Process) bool) ([]Info, error) {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	var found []Info
	for _, p := range all {
		if !match(p) {
			continue
		}
		info := Info{PID: int(p.Pid)}
		if name, err := p.NameWithContext(ctx); err == nil {
			info.Name = name
		}
		if dir, err := p.CwdWithContext(ctx); err == nil {
			info.Dir = dir
		}
		if args, err := p.CmdlineSliceWithContext(ctx); err == nil {
			info.Args = args
		}
		if millis, err := p.CreateTimeWithContext(ctx); err == nil {
			info.StartedAt = time.UnixMilli(millis)
		}
		found = append(found, info)
	}
	return found, nil
}
