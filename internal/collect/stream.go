package collect

import (
	"bufio"
	"io"
	"os"

	"github.com/n1k0s1/grit/internal/model"
)

// Stream is a collector that parses its files a line at a time. The daemon
// keeps one LineState per file and feeds it only the lines appended since
// the last pass, so a 200 MB transcript is read once, not every 30 seconds.
type Stream interface {
	Collector
	Start(path string) LineState
}

// LineState is the parse state of one file.
type LineState interface {
	Line(b []byte)
	Sessions() []model.Session
}

// parseAll parses a whole file through a LineState.
func parseAll(path string, st LineState) ([]model.Session, error) {
	err := eachLine(path, st.Line)
	return st.Sessions(), err
}

// Tail feeds st the complete lines of path from offset on and returns the
// offset after the last complete line. A final line without a newline is
// left for next time, since the tool may still be writing it.
func Tail(path string, offset int64, st LineState) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	r := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			offset += int64(len(line))
			if len(line) > 1 {
				st.Line(line)
			}
		}
		if err == io.EOF {
			return offset, nil
		}
		if err != nil {
			return offset, err
		}
	}
}

// maxCommand caps stored shell commands; heredocs can be huge and only the
// start is useful to a reviewer.
const maxCommand = 1000

// addEvent appends an event, folding a run of plain agent events into one
// that keeps the latest time. Scoring only needs when a run ended, so this
// changes no number and keeps memory flat on long transcripts.
func addEvent(s *model.Session, e model.Event) {
	if r := []rune(e.Command); len(r) > maxCommand {
		e.Command = string(r[:maxCommand]) + "..."
	}
	if n := len(s.Events); n > 0 && e.Kind == model.Agent && e.Text == "" {
		last := &s.Events[n-1]
		if last.Kind == model.Agent && last.Text == "" {
			if e.At.After(last.At) {
				last.At = e.At
			}
			return
		}
	}
	s.Events = append(s.Events, e)
}
