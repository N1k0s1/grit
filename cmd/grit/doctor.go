package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/n1k0s1/grit/internal/activity"
	"github.com/n1k0s1/grit/internal/autostart"
	"github.com/n1k0s1/grit/internal/collect"
	"github.com/n1k0s1/grit/internal/scan"
)

// otherTools are AI tools grit can see installed but cannot read yet.
var otherTools = []struct{ name, path string }{
	{"Cursor", ".cursor"},
	{"Gemini CLI", ".gemini"},
	{"Windsurf", ".codeium"},
	{"OpenCode", filepath.Join(".local", "share", "opencode")},
}

func check(ok bool) string {
	if ok {
		return "ok  "
	}
	return "--  "
}

func doctor() {
	home := collect.HomeDir()
	fmt.Printf("grit doctor (%s/%s)\n\n", runtime.GOOS, runtime.GOARCH)

	fmt.Println("AI tools")
	since := time.Now().AddDate(0, 0, -30)
	rs, _ := scan.New(home).Run(since)
	sessions := map[string]int{}
	for _, r := range rs {
		sessions[r.Harness]++
	}
	for _, c := range collect.All() {
		n := len(c.Paths(home))
		label := map[string]string{"claude-code": "Claude Code", "codex": "Codex", "hooks": "Hook events (grit hook)"}[c.Name()]
		if n == 0 {
			fmt.Printf("  %s%-24s not found\n", check(false), label)
			continue
		}
		count := sessions[c.Name()]
		if c.Name() == "hooks" {
			count = 0
			for h, k := range sessions {
				if len(h) > 5 && h[:5] == "hook:" {
					count += k
				}
			}
		}
		fmt.Printf("  %s%-24s %d log files, %d sessions in the last 30 days\n", check(true), label, n, count)
	}
	for _, t := range otherTools {
		if _, err := os.Stat(filepath.Join(home, t.path)); err == nil {
			fmt.Printf("  %s%-24s installed, not read yet (use a hook recipe, see README)\n", check(false), t.name)
		}
	}
	if n := sessions["manual"]; n > 0 {
		fmt.Printf("  %s%-24s %d sessions of hand-written code\n", check(true), "Hand edits", n)
	}

	fmt.Println("\nActivity signal")
	d, src, err := activity.Idle()
	if err != nil {
		fmt.Printf("  %sno idle signal on this system: sessions use the v1 rules and are lower trust\n", check(false))
	} else {
		fmt.Printf("  %s%s, last input %s ago (seconds only, never keys)\n", check(true), src, d.Round(time.Second))
	}
	r := activity.Log{Dir: activity.DefaultDir(home)}.Reader()
	seen := 0
	now := time.Now().UTC()
	for t := now.Add(-24 * time.Hour); t.Before(now); t = t.Add(time.Minute) {
		if r.Seen(t) {
			seen++
		}
	}
	fmt.Printf("  %ssampled %d of the last 1440 minutes\n", check(seen > 0), seen)

	fmt.Println("\nBackground")
	pid, running := daemonPID()
	if running {
		fmt.Printf("  %sdaemon running (pid %d)\n", check(true), pid)
	} else {
		fmt.Printf("  %sdaemon not running: start it with grit install, or grit daemon in a terminal\n", check(false))
	}
	if ok, where := autostart.Installed(home); ok {
		fmt.Printf("  %sstarts at login: %s\n", check(true), where)
	} else {
		fmt.Printf("  %sdoes not start at login: run grit install\n", check(false))
	}

	fmt.Println("\nAccount")
	c := loadConfig()
	if c.Server == "" || c.Key == "" {
		fmt.Printf("  %snot logged in: tracking stays on this machine\n", check(false))
	} else {
		fmt.Printf("  %slogged in as %s on %s\n", check(true), c.Username, c.Server)
	}

	fmt.Println("\nPrivacy")
	excl := scan.LoadExclude(home)
	sort.Strings(excl)
	if len(excl) == 0 {
		fmt.Println("  no excluded folders (grit exclude add ~/path)")
	}
	for _, p := range excl {
		fmt.Println("  excluded: " + p)
	}
}
