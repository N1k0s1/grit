// Command grit is the client. It reads your AI agents' session logs, works
// out how much of that time was you, and syncs the numbers to a grit server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/n1k0s1/grit/internal/activity"
	"github.com/n1k0s1/grit/internal/collect"
	"github.com/n1k0s1/grit/internal/model"
	"github.com/n1k0s1/grit/internal/redact"
	"github.com/n1k0s1/grit/internal/scan"
)

const usage = `grit: time tracking for building with AI

  grit                      today and this week, per project (offline)
  grit login -server URL -user NAME
  grit sync                 send logged hours (numbers only, no prompts)
  grit daemon [-every 30s]  run in the background: sample activity, watch
                            edits, read new log lines, sync (alias: watch)
  grit sessions [-days 7]   list sessions with credited time
  grit explain ID           show how every minute of a session was counted
  grit submit PROJECT [-note TEXT] [-dry]
                            send a project's hours for review, with redacted prompts
  grit hook -harness NAME -kind prompt|agent|tool|stop
                            for harness hooks: reads the hook's JSON on stdin
  grit exclude [add|rm PATH]
                            folders grit never reads, walks or syncs
  grit activity [-sample]   idle signal status and today's active minutes
                            (-sample keeps recording, as watch does)
`

type config struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Key      string `json:"key"`
}

func gritDir() string { return filepath.Join(collect.HomeDir(), ".grit") }

func loadConfig() config {
	var c config
	b, err := os.ReadFile(filepath.Join(gritDir(), "config.json"))
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if v := os.Getenv("GRIT_SERVER"); v != "" {
		c.Server = v
	}
	if v := os.Getenv("GRIT_KEY"); v != "" {
		c.Key = v
	}
	return c
}

func saveJSON(name string, v any) error {
	if err := os.MkdirAll(gritDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(filepath.Join(gritDir(), name), b, 0o600)
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "grit: "+format+"\n", a...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		status()
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "login":
		login(args)
	case "sync":
		n, err := syncOnce(scan.New(collect.HomeDir()), loadConfig(), true)
		if err != nil {
			die("%v", err)
		}
		fmt.Printf("synced %d changed sessions\n", n)
	case "daemon", "watch":
		daemon(args)
	case "sessions":
		sessions(args)
	case "explain":
		explain(args)
	case "submit":
		submit(args)
	case "hook":
		hook(args)
	case "activity":
		activityCmd(args)
	case "exclude":
		excludeCmd(args)
	case "status":
		status()
	case "demo-live":
		demoLive(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func dur(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	h, m := sec/3600, (sec%3600)/60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %02dm", h, m)
}

func results(days int) []*model.Result {
	rs, err := scan.New(collect.HomeDir()).Run(time.Now().AddDate(0, 0, -days))
	if err != nil {
		die("%v", err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start.After(rs[j].Start) })
	return rs
}

func status() {
	rs := results(7)
	if len(rs) == 0 {
		fmt.Println("No AI sessions found in the last 7 days. grit reads Claude Code and Codex logs automatically; other tools report through `grit hook`.")
		return
	}
	y, m, d := time.Now().Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	type agg struct{ today, week, wall int }
	by := map[string]*agg{}
	var today, week, wall int
	for _, r := range rs {
		a := by[r.Project]
		if a == nil {
			a = &agg{}
			by[r.Project] = a
		}
		a.week += r.CreditedSec
		a.wall += r.WallSec
		week += r.CreditedSec
		wall += r.WallSec
		if r.Start.After(midnight) {
			a.today += r.CreditedSec
			today += r.CreditedSec
		}
	}
	names := make([]string, 0, len(by))
	for k := range by {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return by[names[i]].week > by[names[j]].week })
	fmt.Printf("\n  today %s   this week %s   focus %d%%\n\n", dur(today), dur(week), pct(week, wall))
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 3, ' ', 0)
	fmt.Fprintln(w, "  PROJECT\tTODAY\tWEEK\tSESSION TIME")
	for _, n := range names {
		a := by[n]
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", n, dur(a.today), dur(a.week), dur(a.wall))
	}
	w.Flush()
	fmt.Println()
}

func pct(a, b int) int {
	if b == 0 {
		return 0
	}
	return a * 100 / b
}

func sessions(args []string) {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	days := fs.Int("days", 7, "how far back")
	_ = fs.Parse(args)
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTARTED\tPROJECT\tTOOL\tPROMPTS\tEDITS AI/HAND\tSESSION\tAGENT\tCREDITED\tFLAGS")
	for _, r := range results(*days) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d/%d\t%s\t%s\t%s\t%s\n", short(r.SessionID), r.Start.Local().Format("Mon 02 Jan 15:04"),
			r.Project, r.Harness, r.Prompts, r.AgentEdits, r.HandEdits, dur(r.WallSec), dur(r.AgentSec), dur(r.CreditedSec), strings.Join(r.Flags, ","))
	}
	w.Flush()
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func explain(args []string) {
	if len(args) < 1 {
		die("usage: grit explain SESSION_ID")
	}
	for _, r := range results(90) {
		if !strings.HasPrefix(r.SessionID, args[0]) {
			continue
		}
		fmt.Printf("\n%s  %s  %s\n", r.Project, r.Harness, r.Start.Local().Format("Mon 02 Jan 15:04"))
		fmt.Printf("session %s, agent ran %s, credited %s", dur(r.WallSec), dur(r.AgentSec), dur(r.CreditedSec))
		if r.OverlapSec > 0 {
			fmt.Printf(" (%s removed: overlapped another session)", dur(r.OverlapSec))
		}
		fmt.Println()
		if r.Signal == "activity" {
			fmt.Println("scored with keyboard and mouse activity: only minutes with input count")
		} else {
			fmt.Println("no activity data for this session: scored by timestamps only (lower trust)")
		}
		if len(r.Flags) > 0 {
			fmt.Println("flags:", strings.Join(r.Flags, ", "))
		}
		fmt.Println()
		for _, t := range r.Turns {
			text := strings.ReplaceAll(t.PromptText, "\n", " ")
			if len([]rune(text)) > 70 {
				text = string([]rune(text)[:70]) + "..."
			}
			fmt.Printf("#%d %s  %q\n", t.Index, t.PromptAt.Local().Format("15:04:05"), text)
			fmt.Printf("   before: %-7s %s\n", dur(t.ThinkSec), t.ThinkRule)
			fmt.Printf("   run:    %-7s of %s, %s\n", dur(t.AgentCreditSec), dur(t.AgentSec), t.AgentRule)
		}
		for _, c := range r.Commits {
			fmt.Printf("commit %s %s (+%d -%d)\n", c.SHA[:7], c.Subject, c.Additions, c.Deletions)
		}
		return
	}
	die("no session starting with %q in the last 90 days", args[0])
}

// ---- server calls ----

func call(c config, method, path string, body any, out any) error {
	if c.Server == "" {
		return fmt.Errorf("not logged in: run `grit login -server URL -user NAME`")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimRight(c.Server, "/")+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grit-client/0.1")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func login(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "grit server URL")
	user := fs.String("user", "", "username to create")
	key := fs.String("key", "", "existing API key (skips signup)")
	_ = fs.Parse(args)
	c := config{Server: *server, Username: *user, Key: *key}
	if c.Key == "" {
		if *user == "" {
			die("-user is required")
		}
		var out struct{ Key string }
		if err := call(c, "POST", "/api/v1/signup", map[string]string{"username": *user}, &out); err != nil {
			die("%v", err)
		}
		c.Key = out.Key
	}
	if err := saveJSON("config.json", c); err != nil {
		die("%v", err)
	}
	fmt.Printf("logged in as %s on %s. Your profile: %s/u/%s\n", c.Username, c.Server, strings.TrimRight(c.Server, "/"), c.Username)
}

// syncOnce uploads sessions whose numbers changed since the last sync.
func syncOnce(sc *scan.Scanner, c config, verbose bool) (int, error) {
	rs, err := sc.Run(time.Now().AddDate(0, 0, -30))
	if err != nil {
		return 0, err
	}
	return sendChanged(rs, c, verbose)
}

// sendChanged uploads the results whose numbers changed since the last sync.
func sendChanged(rs []*model.Result, c config, verbose bool) (int, error) {
	state := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(gritDir(), "synced.json")); err == nil {
		_ = json.Unmarshal(b, &state)
	}
	var send []model.Result
	for _, r := range rs {
		fp := fmt.Sprintf("%d|%d|%d|%d", r.End.Unix(), r.CreditedSec, r.Prompts, len(r.Commits))
		if state[r.SessionID] == fp {
			continue
		}
		send = append(send, r.Public())
		state[r.SessionID] = fp
	}
	if len(send) == 0 {
		return 0, nil
	}
	if err := call(c, "POST", "/api/v1/sessions", map[string]any{"sessions": send}, nil); err != nil {
		return 0, err
	}
	if verbose {
		for _, r := range send {
			fmt.Printf("  %s %-24s %s credited\n", short(r.SessionID), r.Project, dur(r.CreditedSec))
		}
	}
	return len(send), saveJSON("synced.json", state)
}

func submit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	note := fs.String("note", "", "a line for the reviewer")
	dry := fs.Bool("dry", false, "print exactly what would be sent, send nothing")
	days := fs.Int("days", 90, "how far back")
	var project string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		project, args = args[0], args[1:]
	}
	_ = fs.Parse(args)
	if project == "" {
		die("usage: grit submit PROJECT [-note TEXT] [-dry]")
	}
	var b struct {
		Project  string         `json:"project"`
		Remote   string         `json:"remote"`
		Note     string         `json:"note"`
		Sessions []model.Result `json:"sessions"`
	}
	b.Project, b.Note = project, *note
	total := 0
	for _, r := range results(*days) {
		if r.Project != project {
			continue
		}
		x := *r
		x.Turns = append([]model.Turn(nil), r.Turns...)
		for i := range x.Turns {
			x.Turns[i].PromptText = redact.Text(x.Turns[i].PromptText)
			cmds := make([]string, len(x.Turns[i].Commands))
			for j, c := range x.Turns[i].Commands {
				cmds[j] = redact.Text(c)
			}
			x.Turns[i].Commands = cmds
		}
		if x.Remote != "" {
			b.Remote = x.Remote
		}
		total += x.CreditedSec
		b.Sessions = append(b.Sessions, x)
	}
	if len(b.Sessions) == 0 {
		die("no sessions for project %q", project)
	}
	if *dry {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(b)
		return
	}
	c := loadConfig()
	var out struct {
		ID         int64  `json:"id"`
		ReviewPath string `json:"review_path"`
	}
	if err := call(c, "POST", "/api/v1/submissions", b, &out); err != nil {
		die("%v", err)
	}
	fmt.Printf("submitted %s across %d sessions for review: %s%s\n", dur(total), len(b.Sessions), strings.TrimRight(c.Server, "/"), out.ReviewPath)
}

func hook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ExitOnError)
	harness := fs.String("harness", "", "harness name, e.g. cursor")
	kind := fs.String("kind", "agent", "prompt, agent, tool or stop")
	_ = fs.Parse(args)
	if *harness == "" {
		die("-harness is required")
	}
	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if err := collect.AppendHook(collect.HomeDir(), collect.FromHookInput(*harness, *kind, raw)); err != nil {
		die("%v", err)
	}
}

// startSampler records keyboard and mouse activity (seconds since the last
// input, never what was typed) into ~/.grit/activity until ctx ends.
func startSampler(ctx context.Context) {
	s := &activity.Sampler{Log: activity.Log{Dir: activity.DefaultDir(collect.HomeDir())}}
	warned := false
	s.Run(ctx, func(err error) {
		if !warned {
			fmt.Fprintln(os.Stderr, "activity:", err, "(sessions are scored with the v1 rules and marked lower trust)")
			warned = true
		}
	})
}

func activityCmd(args []string) {
	fs := flag.NewFlagSet("activity", flag.ExitOnError)
	sample := fs.Bool("sample", false, "keep sampling every 15 seconds")
	_ = fs.Parse(args)
	d, src, err := activity.Idle()
	if err != nil {
		fmt.Println("idle signal: unknown on this system. Sessions are scored with the v1 rules and marked lower trust.")
	} else {
		fmt.Printf("idle signal: %s, last input %s ago\n", src, d.Round(time.Second))
	}
	log := activity.Log{Dir: activity.DefaultDir(collect.HomeDir())}
	r := log.Reader()
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var active, seen int
	for t := start; t.Before(now); t = t.Add(time.Minute) {
		if r.Active(t) {
			active++
		}
		if r.Seen(t) {
			seen++
		}
	}
	fmt.Printf("today (UTC): %d minutes with input, %d minutes sampled\n", active, seen)
	if *sample {
		fmt.Println("sampling every 15s, Ctrl-C to stop")
		startSampler(context.Background())
	}
}

func excludeCmd(args []string) {
	home := collect.HomeDir()
	if len(args) == 2 && (args[0] == "add" || args[0] == "rm") {
		var err error
		if args[0] == "add" {
			err = scan.AddExclude(home, args[1])
		} else {
			err = scan.RemoveExclude(home, args[1])
		}
		if err != nil {
			die("%v", err)
		}
	} else if len(args) != 0 {
		die("usage: grit exclude [add PATH | rm PATH]")
	}
	pats := scan.LoadExclude(home)
	if len(pats) == 0 {
		fmt.Println("Nothing excluded. Add a folder with: grit exclude add ~/path")
		return
	}
	fmt.Println("grit never reads, walks or syncs anything in:")
	for _, p := range pats {
		fmt.Println("  " + p)
	}
}
