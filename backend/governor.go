package backend

import (
	"sync"
	"time"
)

// loopbackInitBackoffBase/Max bound the exponential backoff between
// loopback client (re-)construction attempts ("init failure
// surfaces in status — never wedging the mount"). There is no
// refresh-interval-style config knob to cap against here (unlike the
// sibling engine's backoffDelay), so the ceiling is a fixed constant.
const (
	loopbackInitBackoffBase = time.Second
	loopbackInitBackoffMax  = 5 * time.Minute
)

// loopbackGovernor paces retries of loopback client construction
// (sibling's clientInitState/backoffDelay pattern, governor.go): each
// consecutive construction failure doubles the delay before the next
// attempt is allowed, capped at loopbackInitBackoffMax; a success
// clears it. Independent of Backend's own now field so it's testable
// in isolation.
type loopbackGovernor struct {
	mu  sync.Mutex
	now func() time.Time

	consecutiveFailures int
	nextAllowedAt       time.Time
	lastErr             string
}

func newLoopbackGovernor(now func() time.Time) *loopbackGovernor {
	return &loopbackGovernor{now: now}
}

// allowed reports whether a construction attempt may run right now.
func (g *loopbackGovernor) allowed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.now().Before(g.nextAllowedAt)
}

// recordResult classifies a construction outcome: nil clears all
// retry state; an error bumps the failure count and schedules the
// next allowed attempt via exponential backoff.
func (g *loopbackGovernor) recordResult(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil {
		g.consecutiveFailures = 0
		g.nextAllowedAt = time.Time{}
		g.lastErr = ""
		return
	}
	g.consecutiveFailures++
	g.lastErr = err.Error()
	g.nextAllowedAt = g.now().Add(loopbackBackoffDelay(g.consecutiveFailures))
}

// snapshot returns the read-only state Task 8's status endpoint will
// surface ("init failure surfaces in status").
func (g *loopbackGovernor) snapshot() (consecutiveFailures int, lastErr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.consecutiveFailures, g.lastErr
}

// loopbackBackoffDelay is 1s * 2^(consecutiveFailures-1), capped at
// loopbackInitBackoffMax (sibling's backoffDelay pattern).
func loopbackBackoffDelay(consecutiveFailures int) time.Duration {
	d := loopbackInitBackoffBase
	for i := 1; i < consecutiveFailures && d < loopbackInitBackoffMax; i++ {
		d *= 2
	}
	if d > loopbackInitBackoffMax {
		d = loopbackInitBackoffMax
	}
	return d
}
