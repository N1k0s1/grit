// Package activity measures whether a person is at the keyboard. It reads
// one number from the operating system: how long since the last keyboard or
// mouse input. It never sees which keys were pressed or where the mouse went.
//
// The sampler (sampler.go) turns that number into a bitmap of active minutes
// per day, which the scorer uses to credit only minutes with real input.
package activity

import (
	"errors"
	"time"
)

// ErrUnknown means this machine offers no idle signal grit can read (for
// example a Wayland desktop other than GNOME, or a headless server).
var ErrUnknown = errors.New("activity: no idle signal on this system")

// Idle returns the time since the last keyboard or mouse input and the name
// of the source it came from (for `grit activity` and reviewers). It returns
// ErrUnknown when no source works.
func Idle() (time.Duration, string, error) { return idle() }
