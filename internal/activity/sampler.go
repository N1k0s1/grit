package activity

import (
	"context"
	"time"
)

// SampleEvery is how often the sampler asks the OS for the idle time.
const SampleEvery = 15 * time.Second

// Sampler turns idle readings into the activity log. Each reading gives the
// time of the last input (now minus idle). If that is newer than the
// previous reading, the minute it fell in is marked active. Input between
// two readings in different minutes can be missed, so the log can only
// under-count, never over-count.
type Sampler struct {
	Log      Log
	Interval time.Duration                         // defaults to SampleEvery
	Now      func() time.Time                      // defaults to time.Now
	Idle     func() (time.Duration, string, error) // defaults to Idle

	prev time.Time
}

func (s *Sampler) defaults() {
	if s.Interval == 0 {
		s.Interval = SampleEvery
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Idle == nil {
		s.Idle = Idle
	}
}

// Sample takes one reading and records it. With no idle signal it records
// nothing and returns ErrUnknown, so those minutes stay "no data".
func (s *Sampler) Sample() error {
	s.defaults()
	now := s.Now()
	idle, _, err := s.Idle()
	if err != nil {
		s.prev = time.Time{}
		return err
	}
	last := now.Add(-idle)
	fresh := false
	if s.prev.IsZero() {
		fresh = idle <= s.Interval
	} else {
		fresh = last.After(s.prev)
	}
	s.prev = now
	if !fresh {
		last = time.Time{}
	}
	return s.Log.Record(now, last)
}

// Run samples until ctx is done. Errors (no signal, disk full) are passed to
// onErr if set and sampling carries on.
func (s *Sampler) Run(ctx context.Context, onErr func(error)) {
	s.defaults()
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		if err := s.Sample(); err != nil && onErr != nil {
			onErr(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
