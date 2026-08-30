package tasks

import (
	"sync"
	"time"
)

// Clock keeps scheduling tests independent from wall-clock sleeps.
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                             { return time.Now() }
func (realClock) After(delay time.Duration) <-chan time.Time { return time.After(delay) }

// FakeClock is exported for deterministic scheduler and integration tests.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeWaiter
}

type fakeWaiter struct {
	at      time.Time
	channel chan time.Time
}

func NewFakeClock(now time.Time) *FakeClock { return &FakeClock{now: now} }

func (clock *FakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *FakeClock) After(delay time.Duration) <-chan time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	channel := make(chan time.Time, 1)
	if delay <= 0 {
		channel <- clock.now
		return channel
	}
	clock.waiters = append(clock.waiters, &fakeWaiter{at: clock.now.Add(delay), channel: channel})
	return channel
}

func (clock *FakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	now := clock.now
	remaining := clock.waiters[:0]
	ready := []*fakeWaiter{}
	for _, waiter := range clock.waiters {
		if waiter.at.After(now) {
			remaining = append(remaining, waiter)
		} else {
			ready = append(ready, waiter)
		}
	}
	clock.waiters = remaining
	clock.mu.Unlock()
	for _, waiter := range ready {
		waiter.channel <- now
	}
}
