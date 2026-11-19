package activity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var day0 = time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)

func TestDaySetGetCount(t *testing.T) {
	var d Day
	for _, m := range []int{0, 1, 7, 8, 719, 1439} {
		d.Set(m)
	}
	d.Set(-1)   // ignored
	d.Set(1440) // ignored
	for m := 0; m < MinutesPerDay; m++ {
		want := m == 0 || m == 1 || m == 7 || m == 8 || m == 719 || m == 1439
		if d.Get(m) != want {
			t.Fatalf("minute %d: got %v want %v", m, d.Get(m), want)
		}
	}
	if d.Count() != 6 {
		t.Fatalf("count %d", d.Count())
	}
	if d.Get(-5) || d.Get(5000) {
		t.Fatal("out of range minutes must read as false")
	}
}

func TestDayFileIs180Bytes(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	at := day0.Add(13*time.Hour + 37*time.Minute + 12*time.Second)
	if err := l.Record(at, at); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"2026-11-19", "2026-11-19.seen"} {
		st, err := os.Stat(filepath.Join(l.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() != 180 {
			t.Fatalf("%s is %d bytes, want 180", name, st.Size())
		}
	}
}

func TestRecordAndRead(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	sample := day0.Add(10*time.Hour + 30*time.Second)
	// Sampled at 10:00:30, last input at 09:59:50: minute 09:59 is active,
	// minute 10:00 is seen (the sampler ran) but not active.
	if err := l.Record(sample, sample.Add(-40*time.Second)); err != nil {
		t.Fatal(err)
	}
	// A sample with no recent input only marks the minute as seen.
	quiet := day0.Add(11 * time.Hour)
	if err := l.Record(quiet, time.Time{}); err != nil {
		t.Fatal(err)
	}
	r := l.Reader()
	cases := []struct {
		at           time.Time
		seen, active bool
	}{
		{day0.Add(9*time.Hour + 59*time.Minute + 5*time.Second), true, true}, // the input minute was observed
		{day0.Add(10 * time.Hour), true, false},
		{day0.Add(11*time.Hour + 30*time.Second), true, false},
		{day0.Add(12 * time.Hour), false, false},
		{day0.AddDate(0, 0, 1), false, false}, // no file for that day
	}
	for _, c := range cases {
		if r.Seen(c.at) != c.seen || r.Active(c.at) != c.active {
			t.Errorf("%s: seen=%v active=%v, want %v %v", c.at.Format("15:04:05"), r.Seen(c.at), r.Active(c.at), c.seen, c.active)
		}
	}
}

func TestRecordAcrossMidnight(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	sample := day0.AddDate(0, 0, 1).Add(5 * time.Second) // 00:00:05 next day
	if err := l.Record(sample, sample.Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	r := l.Reader()
	if !r.Active(day0.Add(23*time.Hour + 59*time.Minute)) {
		t.Fatal("input at 23:59:55 should mark the previous day's last minute")
	}
	if !r.Seen(sample) {
		t.Fatal("sample minute should be seen")
	}
}

func TestSamplerMarksMinutes(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	now := day0.Add(14*time.Hour + 10*time.Second)
	idles := []time.Duration{2 * time.Second, 10 * time.Second, 5 * time.Minute}
	i := 0
	s := Sampler{Log: l, Now: func() time.Time { return now }, Idle: func() (time.Duration, string, error) {
		d := idles[i]
		i++
		return d, "fake", nil
	}}
	for range idles {
		if err := s.Sample(); err != nil {
			t.Fatal(err)
		}
		now = now.Add(15 * time.Second)
	}
	r := l.Reader()
	if !r.Active(day0.Add(14 * time.Hour)) {
		t.Fatal("14:00 had input")
	}
	// Third sample at 14:00:40 with 5m idle: last input was 13:55:40, which
	// is older than the gap since the previous sample, so it is not new and
	// must not be marked.
	if r.Active(day0.Add(13*time.Hour + 55*time.Minute)) {
		t.Fatal("stale input must not be marked")
	}
}

func TestSamplerUnknownMarksNothing(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	now := day0.Add(14 * time.Hour)
	s := Sampler{Log: l, Now: func() time.Time { return now }, Idle: func() (time.Duration, string, error) {
		return 0, "", ErrUnknown
	}}
	if err := s.Sample(); err != ErrUnknown {
		t.Fatalf("err %v", err)
	}
	if l.Reader().Seen(now) {
		t.Fatal("no signal means no data, not a quiet minute")
	}
}

func TestSamplerPausedRecordsNothing(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	now := day0.Add(14 * time.Hour)
	s := Sampler{Log: l, Now: func() time.Time { return now }, Paused: func() bool { return true },
		Idle: func() (time.Duration, string, error) { return time.Second, "fake", nil }}
	if err := s.Sample(); err != nil {
		t.Fatal(err)
	}
	if r := l.Reader(); r.Seen(now) || r.Active(now) {
		t.Fatal("a paused sampler must record nothing")
	}
}
