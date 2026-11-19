package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/n1k0s1/grit/internal/collect"
)

// demoLive writes a fake Claude Code transcript a turn at a time, so a demo
// can show `grit watch` picking up a session and the dashboard moving. The
// session replays the last hour in fast forward and never writes future
// timestamps.
func demoLive(args []string) {
	fs := flag.NewFlagSet("demo-live", flag.ExitOnError)
	tick := fs.Duration("tick", 3*time.Second, "real time between turns")
	cwd := fs.String("cwd", "", "working directory to attribute the session to")
	_ = fs.Parse(args)
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}
	id := fmt.Sprintf("live-%d", time.Now().Unix())
	dir := filepath.Join(collect.HomeDir(), ".claude", "projects", "-demo-live")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		die("%v", err)
	}
	defer f.Close()
	prompts := []string{
		"set up a three.js scene with a first person camera and pointer lock",
		"movement feels floaty, add friction and make strafing snappier",
		"the player falls through the floor on spawn, why?",
		"ok that fixed it. now add a simple box map with a few crates to hide behind",
		"add raycast shooting with a crosshair and a hit marker",
		"hits are registering on the wrong crate when two overlap, check the raycast order",
		"continue",
		"add a round timer and a scoreboard overlay on tab",
		"the scoreboard flickers when I hold tab, I think keydown repeats",
		"write a short README section on the controls",
	}
	clock := time.Now().Add(-55 * time.Minute)
	emit := func(m map[string]any) {
		m["timestamp"] = clock.UTC().Format(time.RFC3339Nano)
		m["sessionId"] = id
		m["cwd"] = *cwd
		m["gitBranch"] = "main"
		b, _ := json.Marshal(m)
		_, _ = f.Write(append(b, '\n'))
	}
	for i, p := range prompts {
		emit(map[string]any{"type": "user", "origin": map[string]string{"kind": "human"}, "message": map[string]any{"role": "user", "content": p}})
		for k := 0; k < 2+rand.Intn(3); k++ {
			clock = clock.Add(time.Duration(20+rand.Intn(60)) * time.Second)
			emit(map[string]any{"type": "assistant", "message": map[string]any{"model": "claude-sonnet-5-5", "content": []any{
				map[string]any{"type": "tool_use", "name": "Edit", "input": map[string]string{"file_path": filepath.Join(*cwd, []string{"src/game.js", "src/player.js", "src/ui.js"}[rand.Intn(3)])}},
			}}})
		}
		clock = clock.Add(time.Duration(30+rand.Intn(90)) * time.Second)
		emit(map[string]any{"type": "assistant", "message": map[string]any{"model": "claude-sonnet-5-5", "content": []any{map[string]any{"type": "text", "text": "done"}}}})
		clock = clock.Add(time.Duration(40+rand.Intn(150)) * time.Second)
		if clock.After(time.Now()) {
			clock = time.Now()
		}
		fmt.Printf("turn %d/%d written\n", i+1, len(prompts))
		time.Sleep(*tick)
	}
}
