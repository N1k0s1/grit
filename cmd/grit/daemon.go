package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/n1k0s1/grit/internal/collect"
	"github.com/n1k0s1/grit/internal/edits"
	"github.com/n1k0s1/grit/internal/scan"
)

// memoryLimit is a soft cap on the daemon's Go memory. It runs all day on
// old laptops, so it trades a little CPU for staying small. GOMEMLIMIT in
// the environment overrides it.
const memoryLimit = 16 << 20

// rescoreEvery is the longest the daemon goes without rescoring.
const rescoreEvery = 5 * time.Minute

func pidPath() string { return filepath.Join(gritDir(), "daemon.pid") }

// daemonPID returns the pid in the pid file if that process is running.
func daemonPID() (int, bool) {
	b, err := os.ReadFile(pidPath())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, processAlive(pid)
}

// daemon is the background process: the activity sampler, the edits
// watcher, incremental log reading and sync, in one loop.
func daemon(args []string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	every := fs.Duration("every", 30*time.Second, "how often to look for changes")
	background := fs.Bool("background", false, "detach from the console (used by the Windows login entry)")
	_ = fs.Parse(args)
	if *background {
		hideConsole()
	}
	rotateLog()
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(memoryLimit)
	}

	if pid, ok := daemonPID(); ok && pid != os.Getpid() {
		die("already running (pid %d)", pid)
	}
	if err := os.MkdirAll(gritDir(), 0o700); err == nil {
		_ = os.WriteFile(pidPath(), []byte(strconv.Itoa(os.Getpid())), 0o600)
	}

	home := collect.HomeDir()
	sc := scan.New(home)
	w := &edits.Watcher{}
	snap := &snapshot{}
	if d, err := newDash(home, snap.get); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
	} else if u, err := d.serve(); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
	} else {
		fmt.Println("grit daemon: local dashboard at " + u + " (open it with grit open)")
	}
	go startSampler(context.Background())
	c := loadConfig()
	if c.Server == "" || c.Key == "" {
		fmt.Println("grit daemon: not logged in, so nothing is synced. Tracking stays on this machine.")
	} else {
		fmt.Printf("grit daemon: syncing to %s every %s\n", c.Server, *every)
	}
	var last time.Time
	for {
		if paused, _ := scan.Paused(home); paused {
			time.Sleep(*every) // nothing is sampled, logged or synced
			continue
		}
		// Files written in recently active projects. The first pass over a
		// project is a baseline; later passes log what changed.
		w.SetRoots(sc.Roots())
		changes := w.Poll()
		if err := edits.Append(edits.DefaultDir(home), changes); err != nil {
			fmt.Fprintln(os.Stderr, "edits:", err)
		}
		// Rescore when a log or the working tree changed, and every few
		// minutes anyway so new activity minutes reach the numbers. A quiet
		// machine costs one stat per log file per pass.
		if len(changes) == 0 && !sc.Changed() && time.Since(last) < rescoreEvery {
			time.Sleep(*every)
			continue
		}
		last = time.Now()
		before := sc.BytesRead()
		rs, err := sc.Run(time.Now().AddDate(0, 0, -30))
		if err == nil {
			snap.set(rs)
		}
		if sc.BytesRead()-before > 8<<20 {
			// A big read (the first pass over old logs) leaves garbage the
			// runtime would hand back only slowly. Return it now.
			debug.FreeOSMemory()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "scan:", err)
		} else if c = loadConfig(); c.Server != "" && c.Key != "" {
			if n, err := sendChanged(rs, c, false); err != nil {
				fmt.Fprintln(os.Stderr, "sync:", err)
			} else if n > 0 {
				fmt.Printf("%s synced %d sessions\n", time.Now().Format("15:04:05"), n)
			}
		}
		time.Sleep(*every)
	}
}

// rotateLog keeps the daemon log small: over 1 MB it moves to daemon.log.old.
func rotateLog() {
	if st, err := os.Stat(logPath()); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(logPath(), logPath()+".old")
	}
}
