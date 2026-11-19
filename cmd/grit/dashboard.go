package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n1k0s1/grit/internal/activity"
	"github.com/n1k0s1/grit/internal/collect"
	"github.com/n1k0s1/grit/internal/model"
	"github.com/n1k0s1/grit/internal/scan"
	"github.com/n1k0s1/grit/internal/webstatic"
)

// The local dashboard shows a person their own data, including their own
// prompts, which never leave the machine. It listens on 127.0.0.1 only and
// every request needs a random token, so neither another machine nor
// another website in the same browser can read it.

//go:embed web
var dashFS embed.FS

const cookieName = "grit_local"

type dash struct {
	home    string
	token   string
	host    string // 127.0.0.1:<port>
	results func() []*model.Result
	tpl     map[string]*template.Template
}

// snapshot holds the daemon's latest results for the dashboard.
type snapshot struct {
	mu sync.Mutex
	rs []*model.Result
}

func (s *snapshot) set(rs []*model.Result) { s.mu.Lock(); s.rs = rs; s.mu.Unlock() }
func (s *snapshot) get() []*model.Result   { s.mu.Lock(); defer s.mu.Unlock(); return s.rs }

func dashPath() string { return filepath.Join(gritDir(), "dashboard.json") }

type dashInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func newDash(home string, results func() []*model.Result) (*dash, error) {
	d := &dash{home: home, token: newState(), results: results, tpl: map[string]*template.Template{}}
	funcs := template.FuncMap{
		"dur":  dur,
		"when": func(t time.Time) string { return t.Local().Format("Mon 2 Jan 15:04") },
		"hm":   func(t time.Time) string { return t.Local().Format("15:04:05") },
		"pct": func(a, b int) int {
			if b <= 0 {
				return 0
			}
			return min(100, a*100/b)
		},
		"short": short,
		"join":  strings.Join,
	}
	for _, page := range []string{"today", "sessions", "session", "week", "privacy", "submit", "preview", "done"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(dashFS, "web/layout.html", "web/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", page, err)
		}
		d.tpl[page] = t
	}
	return d, nil
}

// serve starts listening on a random loopback port and records the URL and
// token in ~/.grit/dashboard.json (readable only by the user).
func (d *dash) serve() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	d.host = ln.Addr().String()
	u := "http://" + d.host + "/"
	info, _ := json.Marshal(dashInfo{URL: u, Token: d.token, PID: os.Getpid()})
	if err := os.MkdirAll(gritDir(), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(dashPath(), info, 0o600); err != nil {
		return "", err
	}
	srv := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { log.Println(srv.Serve(ln)) }()
	return u, nil
}

func (d *dash) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(webstatic.FS()))))
	mux.HandleFunc("GET /{$}", d.today)
	mux.HandleFunc("GET /sessions", d.sessions)
	mux.HandleFunc("GET /s/{id}", d.session)
	mux.HandleFunc("GET /week", d.week)
	mux.HandleFunc("GET /privacy", d.privacy)
	mux.HandleFunc("POST /privacy/pause", d.pause)
	mux.HandleFunc("POST /privacy/resume", d.resume)
	mux.HandleFunc("POST /privacy/exclude", d.exclude)
	mux.HandleFunc("POST /privacy/include", d.include)
	mux.HandleFunc("POST /goal", d.goal)
	mux.HandleFunc("GET /submit", d.submitList)
	mux.HandleFunc("GET /submit/{project}", d.submitPreview)
	mux.HandleFunc("POST /submit/{project}", d.submitSend)
	return d.guard(mux)
}

// guard refuses anything not addressed to our loopback host (blocking DNS
// rebinding), anything without the token, and form posts from other
// origins. A first visit with ?token= swaps it for a strict cookie and
// redirects so the token leaves the address bar.
func (d *dash) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if r.Host != d.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if t := r.URL.Query().Get("token"); t != "" && r.Method == http.MethodGet {
			if !d.tokenOK(t) {
				http.Error(w, "wrong token: open the dashboard with grit open", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: d.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			q := r.URL.Query()
			q.Del("token")
			u := *r.URL
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.RequestURI(), http.StatusFound)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !d.tokenOK(c.Value) {
			http.Error(w, "open the dashboard with grit open", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != "http://"+d.host {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (d *dash) tokenOK(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(d.token)) == 1
}

func (d *dash) render(w http.ResponseWriter, page string, data map[string]any) {
	data["Page"] = page
	data["Paused"], data["PausedSince"] = scan.Paused(d.home)
	data["Loading"] = d.results() == nil
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := d.tpl[page].Execute(w, data); err != nil {
		log.Printf("dashboard %s: %v", page, err)
	}
}

func (d *dash) back(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---- pages ----

type projectRow struct {
	Name              string
	Today, Week, Wall int
	Sessions          int
}

func midnight() time.Time {
	y, m, dd := time.Now().Date()
	return time.Date(y, m, dd, 0, 0, 0, 0, time.Local)
}

func (d *dash) today(w http.ResponseWriter, r *http.Request) {
	rs := d.results()
	start := midnight()
	weekAgo := time.Now().AddDate(0, 0, -7)
	by := map[string]*projectRow{}
	var today, week, wall, todayPrompts int
	for _, x := range rs {
		if x.Start.Before(weekAgo) {
			continue
		}
		p := by[x.Project]
		if p == nil {
			p = &projectRow{Name: x.Project}
			by[x.Project] = p
		}
		p.Week += x.CreditedSec
		p.Wall += x.WallSec
		p.Sessions++
		week += x.CreditedSec
		wall += x.WallSec
		if !x.Start.Before(start) {
			p.Today += x.CreditedSec
			today += x.CreditedSec
			todayPrompts += x.Prompts
		}
	}
	rows := make([]*projectRow, 0, len(by))
	for _, p := range by {
		rows = append(rows, p)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Week > rows[j].Week })
	idle, src, err := activity.Idle()
	g := loadGoal()
	d.render(w, "today", map[string]any{
		"Title": "Today", "Today": today, "Week": week, "Wall": wall, "Prompts": todayPrompts, "Rows": rows,
		"Goal": g.WeeklyHours, "GoalSec": int(g.WeeklyHours * 3600), "Signal": src, "SignalErr": err != nil,
		"Idle": idle.Round(time.Second).String(),
	})
}

func sorted(rs []*model.Result) []*model.Result {
	out := append([]*model.Result(nil), rs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	return out
}

func (d *dash) sessions(w http.ResponseWriter, r *http.Request) {
	d.render(w, "sessions", map[string]any{"Title": "Sessions", "Sessions": sorted(d.results())})
}

func (d *dash) session(w http.ResponseWriter, r *http.Request) {
	for _, x := range d.results() {
		if x.SessionID == r.PathValue("id") {
			d.render(w, "session", map[string]any{"Title": x.Project, "S": x})
			return
		}
	}
	http.NotFound(w, r)
}

type dayRow struct {
	Day      time.Time
	Credited int
	Wall     int
	Prompts  int
}

func (d *dash) week(w http.ResponseWriter, r *http.Request) {
	start := midnight().AddDate(0, 0, -6)
	days := make([]dayRow, 7)
	for i := range days {
		days[i].Day = start.AddDate(0, 0, i)
	}
	projects := map[string]int{}
	var total, wall, prompts, hand, agentEdits, handEdits int
	for _, x := range d.results() {
		if x.Start.Before(start) {
			continue
		}
		i := int(x.Start.Local().Sub(start).Hours() / 24)
		if i < 0 || i > 6 {
			continue
		}
		days[i].Credited += x.CreditedSec
		days[i].Wall += x.WallSec
		days[i].Prompts += x.Prompts
		projects[x.Project] += x.CreditedSec
		total += x.CreditedSec
		wall += x.WallSec
		prompts += x.Prompts
		agentEdits += x.AgentEdits
		handEdits += x.HandEdits
		if x.Harness == "manual" {
			hand += x.CreditedSec
		}
	}
	best, top := 0, 1
	for i, dd := range days {
		if dd.Credited > days[best].Credited {
			best = i
		}
		if dd.Credited > top {
			top = dd.Credited
		}
	}
	type pr struct {
		Name string
		Sec  int
	}
	var tops []pr
	for k, v := range projects {
		tops = append(tops, pr{k, v})
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].Sec > tops[j].Sec })
	d.render(w, "week", map[string]any{
		"Title": "This week", "Days": days, "Max": top, "Best": days[best], "Total": total, "Wall": wall,
		"Prompts": prompts, "Hand": hand, "AgentEdits": agentEdits, "HandEdits": handEdits, "Top": tops,
		"Goal": loadGoal().WeeklyHours, "GoalSec": int(loadGoal().WeeklyHours * 3600),
	})
}

// ---- privacy ----

func readSent(limit int) []sentEntryView {
	files, _ := filepath.Glob(filepath.Join(sentDir(), "*.jsonl"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	var out []sentEntryView
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
			var e struct {
				At      time.Time       `json:"at"`
				Server  string          `json:"server"`
				Kind    string          `json:"kind"`
				Payload json.RawMessage `json:"payload"`
			}
			if json.Unmarshal([]byte(lines[i]), &e) != nil {
				continue
			}
			var pretty strings.Builder
			var v any
			_ = json.Unmarshal(e.Payload, &v)
			enc := json.NewEncoder(&pretty)
			enc.SetIndent("", "  ")
			_ = enc.Encode(v)
			n := 0
			if arr, ok := v.([]any); ok {
				n = len(arr)
			} else if m, ok := v.(map[string]any); ok {
				if ss, ok := m["sessions"].([]any); ok {
					n = len(ss)
				}
			}
			out = append(out, sentEntryView{At: e.At, Server: e.Server, Kind: e.Kind, Sessions: n, JSON: pretty.String(), Bytes: len(e.Payload)})
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

type sentEntryView struct {
	At       time.Time
	Server   string
	Kind     string
	Sessions int
	Bytes    int
	JSON     string
}

func (d *dash) privacy(w http.ResponseWriter, r *http.Request) {
	d.render(w, "privacy", map[string]any{
		"Title": "Privacy", "Excluded": readExcludeLines(d.home), "Sent": readSent(30),
	})
}

func readExcludeLines(home string) []string {
	b, err := os.ReadFile(scan.ExcludePath(home))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func (d *dash) pause(w http.ResponseWriter, r *http.Request) {
	if err := scan.Pause(d.home); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.back(w, r, "/privacy")
}

func (d *dash) resume(w http.ResponseWriter, r *http.Request) {
	if err := scan.Resume(d.home); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.back(w, r, "/privacy")
}

func (d *dash) exclude(w http.ResponseWriter, r *http.Request) {
	if p := strings.TrimSpace(r.FormValue("path")); p != "" {
		if err := scan.AddExclude(d.home, p); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	d.back(w, r, "/privacy")
}

func (d *dash) include(w http.ResponseWriter, r *http.Request) {
	if err := scan.RemoveExclude(d.home, r.FormValue("path")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.back(w, r, "/privacy")
}

// ---- goals ----

type goal struct {
	WeeklyHours float64 `json:"weekly_hours"`
}

func loadGoal() goal {
	var g goal
	if b, err := os.ReadFile(filepath.Join(gritDir(), "goals.json")); err == nil {
		_ = json.Unmarshal(b, &g)
	}
	return g
}

func (d *dash) goal(w http.ResponseWriter, r *http.Request) {
	h, err := strconv.ParseFloat(r.FormValue("weekly_hours"), 64)
	if err != nil || h < 0 || h > 168 {
		http.Error(w, "weekly goal must be between 0 and 168 hours", 400)
		return
	}
	if err := saveJSON("goals.json", goal{WeeklyHours: h}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d.back(w, r, "/")
}

// ---- submit ----

func (d *dash) submitList(w http.ResponseWriter, r *http.Request) {
	by := map[string]*projectRow{}
	for _, x := range d.results() {
		p := by[x.Project]
		if p == nil {
			p = &projectRow{Name: x.Project}
			by[x.Project] = p
		}
		p.Week += x.CreditedSec
		p.Sessions++
	}
	var rows []*projectRow
	for _, p := range by {
		rows = append(rows, p)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Week > rows[j].Week })
	c := loadConfig()
	d.render(w, "submit", map[string]any{"Title": "Submit hours", "Rows": rows, "LoggedIn": c.Server != "" && c.Key != "", "Server": c.Server})
}

func (d *dash) submitPreview(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	b, total := buildBundle(d.results(), project, "")
	if len(b.Sessions) == 0 {
		http.NotFound(w, r)
		return
	}
	raw, _ := json.MarshalIndent(b, "", "  ")
	c := loadConfig()
	d.render(w, "preview", map[string]any{
		"Title": "Submit " + project, "Project": project, "B": b, "Total": total, "JSON": string(raw),
		"Bytes": len(raw), "LoggedIn": c.Server != "" && c.Key != "", "Server": c.Server,
		"Action": "/submit/" + url.PathEscape(project),
	})
}

func (d *dash) submitSend(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	b, total := buildBundle(d.results(), project, strings.TrimSpace(r.FormValue("note")))
	if len(b.Sessions) == 0 {
		http.NotFound(w, r)
		return
	}
	u, err := sendBundle(loadConfig(), b)
	data := map[string]any{"Title": "Submitted", "Project": project, "Total": total, "Sessions": len(b.Sessions), "URL": u}
	if err != nil {
		data["Error"] = err.Error()
	}
	d.render(w, "done", data)
}

// ---- grit open ----

// openCmd opens the dashboard of the running daemon, or runs one here
// until Ctrl-C if no daemon is running.
func openCmd(args []string) {
	var info dashInfo
	if b, err := os.ReadFile(dashPath()); err == nil && json.Unmarshal(b, &info) == nil {
		if pid, ok := daemonPID(); ok && pid == info.PID {
			u := info.URL + "?token=" + info.Token
			if err := openBrowser(u); err != nil {
				fmt.Println("Open this in your browser:", u)
			}
			return
		}
	}
	home := collect.HomeDir()
	sc := scan.New(home)
	var mu sync.Mutex
	var cached []*model.Result
	var at time.Time
	d, err := newDash(home, func() []*model.Result {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) > 15*time.Second {
			if rs, err := sc.Run(time.Now().AddDate(0, 0, -90)); err == nil {
				cached, at = rs, time.Now()
			}
		}
		return cached
	})
	if err != nil {
		die("%v", err)
	}
	u, err := d.serve()
	if err != nil {
		die("%v", err)
	}
	full := u + "?token=" + d.token
	fmt.Println("grit dashboard (the daemon is not running, so it runs here until Ctrl-C):")
	fmt.Println("  " + full)
	_ = openBrowser(full)
	select {}
}
