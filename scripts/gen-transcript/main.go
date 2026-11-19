// Command gen-transcript writes a synthetic Claude Code transcript of a
// given size, for measuring the daemon on huge logs. Everything in it is
// generated: no real prompts, paths or names.
//
//	go run ./scripts/gen-transcript -home /tmp/gritbench -mb 200
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	home := flag.String("home", "", "fake home folder to write into (required)")
	mb := flag.Int("mb", 200, "size in megabytes")
	flag.Parse()
	if *home == "" {
		log.Fatal("-home is required")
	}
	dir := filepath.Join(*home, ".claude", "projects", "-bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "bench.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()

	cwd := filepath.Join(*home, "game")
	filler := strings.Repeat("synthetic tool output line for the benchmark ", 180) // about 8 KB
	limit := int64(*mb) << 20
	var written int64
	t := time.Now().UTC().Add(-14 * 24 * time.Hour)
	emit := func(v map[string]any) {
		b, _ := json.Marshal(v)
		b = append(b, '\n')
		n, _ := w.Write(b)
		written += int64(n)
	}
	for turn := 0; written < limit; turn++ {
		emit(map[string]any{"type": "user", "sessionId": "bench", "cwd": cwd, "timestamp": t.Format(time.RFC3339Nano),
			"origin": map[string]any{"kind": "human"},
			"message": map[string]any{"role": "user", "content": fmt.Sprintf("synthetic prompt %d: add feature %d to the game", turn, turn)}})
		for step := 0; step < 20 && written < limit; step++ {
			t = t.Add(3 * time.Second)
			emit(map[string]any{"type": "assistant", "timestamp": t.Format(time.RFC3339Nano), "message": map[string]any{
				"model": "claude-opus-5-5", "content": []any{map[string]any{"type": "tool_use", "name": "Edit",
					"input": map[string]any{"file_path": filepath.Join(cwd, fmt.Sprintf("src/file%d.js", step))}}}}})
			t = t.Add(2 * time.Second)
			emit(map[string]any{"type": "user", "timestamp": t.Format(time.RFC3339Nano), "message": map[string]any{
				"content": []any{map[string]any{"type": "tool_result", "content": filler}}}})
		}
		t = t.Add(90 * time.Second)
	}
	fmt.Printf("wrote %d MB ending %s\n", written>>20, t.Format(time.RFC3339))
}
