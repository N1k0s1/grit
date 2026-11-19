package activity

import (
	"errors"
	"math/bits"
	"os"
	"path/filepath"
	"time"
)

// MinutesPerDay is the number of bits in a Day.
const MinutesPerDay = 1440

// Day is one bit per minute of a UTC day, 180 bytes. Days are UTC so a
// minute means the same thing on the client, the server and across daylight
// saving changes.
type Day [MinutesPerDay / 8]byte

// Set marks a minute (0 to 1439). Out of range minutes are ignored.
func (d *Day) Set(m int) {
	if m >= 0 && m < MinutesPerDay {
		d[m/8] |= 1 << (m % 8)
	}
}

// Get reports whether a minute is marked.
func (d *Day) Get(m int) bool {
	return m >= 0 && m < MinutesPerDay && d[m/8]&(1<<(m%8)) != 0
}

// Count is the number of marked minutes.
func (d *Day) Count() int {
	n := 0
	for _, b := range d {
		n += bits.OnesCount8(b)
	}
	return n
}

// Log is the on-disk activity record, normally ~/.grit/activity. Each UTC
// day has two 180 byte files:
//
//	YYYY-MM-DD       minutes with keyboard or mouse input
//	YYYY-MM-DD.seen  minutes the sampler was running and had a signal
//
// The second file is what separates "no input" from "no data": a minute that
// was never seen tells the scorer nothing, so it falls back to the v1 rules.
type Log struct{ Dir string }

// DefaultDir is where the sampler writes for a given home directory.
func DefaultDir(home string) string { return filepath.Join(home, ".grit", "activity") }

func dayName(t time.Time) string { return t.UTC().Format("2006-01-02") }

func minuteOf(t time.Time) int {
	t = t.UTC()
	return t.Hour()*60 + t.Minute()
}

func (l Log) path(t time.Time, seen bool) string {
	name := dayName(t)
	if seen {
		name += ".seen"
	}
	return filepath.Join(l.Dir, name)
}

func readDay(path string) (Day, bool, error) {
	var d Day
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	copy(d[:], b)
	return d, true, nil
}

// mark sets one minute in one file, writing only when the bit was not set.
func (l Log) mark(t time.Time, seen bool) error {
	p := l.path(t, seen)
	d, _, err := readDay(p)
	if err != nil {
		return err
	}
	m := minuteOf(t)
	if d.Get(m) {
		return nil
	}
	d.Set(m)
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	// Write to a temp file and rename so a reader never sees half a day.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, d[:], 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Record stores one sample: the sampler ran at sampleAt, and the last input
// it saw was at lastInput (zero if there was no new input since the
// previous sample).
func (l Log) Record(sampleAt, lastInput time.Time) error {
	if err := l.mark(sampleAt, true); err != nil {
		return err
	}
	if lastInput.IsZero() {
		return nil
	}
	// The input minute was observed too: the sampler saw evidence of it.
	if err := l.mark(lastInput, true); err != nil {
		return err
	}
	return l.mark(lastInput, false)
}

// Reader answers per-minute questions, loading each day file once. It
// satisfies score.Activity.
type Reader struct {
	log  Log
	days map[string]*[2]Day // [0] active, [1] seen
}

// Reader returns a fresh reader. Make a new one per scan so new samples show up.
func (l Log) Reader() *Reader { return &Reader{log: l, days: map[string]*[2]Day{}} }

func (r *Reader) day(t time.Time) *[2]Day {
	k := dayName(t)
	if d, ok := r.days[k]; ok {
		return d
	}
	var d [2]Day
	d[0], _, _ = readDay(r.log.path(t, false))
	d[1], _, _ = readDay(r.log.path(t, true))
	r.days[k] = &d
	return &d
}

// Active reports whether there was keyboard or mouse input in t's minute.
func (r *Reader) Active(t time.Time) bool { return r.day(t)[0].Get(minuteOf(t)) }

// Seen reports whether the sampler was running with a signal in t's minute.
func (r *Reader) Seen(t time.Time) bool { return r.day(t)[1].Get(minuteOf(t)) }
