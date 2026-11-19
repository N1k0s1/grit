//go:build darwin

package activity

import (
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/ebitengine/purego"
)

// macOS: CGEventSourceSecondsSinceLastEventType from CoreGraphics, called
// through purego so no cgo is needed. If CoreGraphics cannot be loaded, fall
// back to the HIDIdleTime property that `ioreg -c IOHIDSystem` prints.

const (
	cgEventSourceStateCombinedSessionState = 0
	cgAnyInputEventType                    = ^uint32(0)
)

var (
	cgOnce    sync.Once
	cgSeconds func(state int32, eventType uint32) float64
)

func loadCG() {
	defer func() { _ = recover() }() // a missing symbol must not crash the client
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	var fn func(int32, uint32) float64
	purego.RegisterLibFunc(&fn, lib, "CGEventSourceSecondsSinceLastEventType")
	cgSeconds = fn
}

var hidIdle = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)

func idle() (time.Duration, string, error) {
	cgOnce.Do(loadCG)
	if cgSeconds != nil {
		s := cgSeconds(cgEventSourceStateCombinedSessionState, cgAnyInputEventType)
		if s >= 0 {
			return time.Duration(s * float64(time.Second)), "coregraphics", nil
		}
	}
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem", "-d", "4").Output()
	if err != nil {
		return 0, "", ErrUnknown
	}
	m := hidIdle.FindSubmatch(out)
	if m == nil {
		return 0, "", ErrUnknown
	}
	ns, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return 0, "", ErrUnknown
	}
	return time.Duration(ns), "ioreg", nil
}
