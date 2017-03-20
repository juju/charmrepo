// Copyright 2017 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package iomon

import (
	"fmt"
	"sync"
	"time"
	
	"github.com/juju/utils/clock"

	tomb "gopkg.in/tomb.v2"
)

const DefaultUpdateInterval = time.Second

// Monitor holds a monitor that continually updates
// a status value with the current progress of some
// long IO operation.
type Monitor struct {
	tomb tomb.Tomb

	p       Params

	currentStatus Status

	mu      sync.Mutex
	current int64
}

// Params holds the parameters for creating a new monitor.
type Params struct {
	// Size holds the total size of the transfer.
	Size int64

	// Setter is used to set the current status of
	// the transfer.
	Setter StatusSetter

	// UpdateInterval controls how often a status update will be
	// sent. It it's zero, DefaultUpdateInterval will be used.
	UpdateInterval time.Duration

	// Clock is used as a source of timing information.
	// If it is nil, the global time will be used.
	Clock clock.Clock
}

// New returns a new running monitor
// using the given parameters. The Monitor
// should be stopped when the transfer is complete.
func New(p Params) *Monitor {
	if p.UpdateInterval == 0 {
		p.UpdateInterval = DefaultUpdateInterval
	}
	if p.Clock == nil {
		p.Clock = clock.WallClock
	}
	m := &Monitor{
		p: p,
	}
	m.tomb.Go(m.run)
	return m
}

// Kill kills the monitor but does not wait for it to exit.
func (m *Monitor) Kill() {
	m.tomb.Kill(nil)
}

// Wait waits for the monitor to exit. When this
// returns, SetStatus will no longer be called.
func (m *Monitor) Wait() error {
	m.tomb.Wait()
	return nil
}

func (m *Monitor) Update(current int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = current
}

// Status indicates the current status of the I/O transfer.
type Status struct {
	Current int64
	Total   int64

	// TODO add rate, expected time
}

// StatusSetter is used to indicate the current progress status.
type StatusSetter interface {
	SetStatus(s Status)
}

func (m *Monitor) run() error {
	for {
		m.setStatus()
		select {
		case <-m.p.Clock.After(m.p.UpdateInterval):
		case <-m.tomb.Dying():
			// Always set the final status when finishing.
			m.setStatus()
			return nil
		}
	}
}

func (m *Monitor) setStatus() {
	m.mu.Lock()
	current := m.current
	m.mu.Unlock()
	status := Status{
		Current: current,
		Total:   m.p.Size,
	}
	if status != m.currentStatus {
		m.p.Setter.SetStatus(status)
		m.currentStatus = status
	}
}

const (
	KiB = 1024
	MiB = 1024 * KiB
	GiB = 1024 * MiB
)

func FormatByteCount(n int64) string {
	switch {
	case n < 10*MiB:
		return fmt.Sprintf("%.0fKiB", float64(n)/KiB)
	case n < 10*GiB:
		return fmt.Sprintf("%.1fMiB", float64(n)/MiB)
	default:
		return fmt.Sprintf("%.1fGiB", float64(n)/GiB)
	}
}
