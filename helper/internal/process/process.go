// Package process inspects running processes. Each detector declares the
// small interface it needs from System, so it can be tested without real
// processes.
package process

import (
	"context"
	"time"
)

// Info describes a running process.
type Info struct {
	PID       int
	Name      string   // executable name, at most 16 bytes on macOS
	Dir       string   // working directory, empty if not readable
	Args      []string // command line, program first; empty if not readable
	StartedAt time.Time
}

// System inspects the processes of the local operating system.
type System struct{}

// StartTime returns when the process started, or false if it is not running.
func (System) StartTime(ctx context.Context, pid int) (time.Time, bool) {
	return startTime(ctx, pid)
}

// FindByName lists running processes whose executable name is exactly name.
func (System) FindByName(ctx context.Context, name string) ([]Info, error) {
	return findByName(ctx, name)
}

// FindByNamePrefix lists running processes whose executable name starts
// with prefix, for programs that put their version in the name.
func (System) FindByNamePrefix(ctx context.Context, prefix string) ([]Info, error) {
	return findByNamePrefix(ctx, prefix)
}

// Children lists the running processes whose parent is one of parents.
func (System) Children(ctx context.Context, parents []int) ([]Info, error) {
	return children(ctx, parents)
}
