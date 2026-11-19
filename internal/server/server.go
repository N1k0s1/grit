// Package server is gritd's HTTP layer: the API the client syncs to, the
// public dashboard, and the reviewer audit view.
package server

import (
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n1k0s1/grit/internal/attest"
	"github.com/n1k0s1/grit/internal/model"
	"github.com/n1k0s1/grit/internal/store"
)

//go:embed web
var webFS embed.FS

// Server holds dependencies.
type Server struct {
	Store       *store.Store
	ReviewerKey string // empty means demo mode: review pages are open
	tpl         map[string]*template.Template
	hub         *hub
}

// New builds a Server.
func New(st *store.Store, reviewerKey string) (*Server, error) {
	s := &Server{Store: st, ReviewerKey: reviewerKey, hub: newHub()}
	funcs := template.FuncMap{
		"dur":   Dur,
		"hours": func(sec int) string { return fmt.Sprintf("%.1f", float64(sec)/3600) },
		"pct": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a * 100 / b
		},
		"json": func(v any) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
		"short": func(s string) string {
			if len(s) > 7 {
				return s[:7]
			}
			return s
		},
		"when": func(t time.Time) string { return t.UTC().Format("Mon 2 Jan 15:04 UTC") },
		"unix": func(u int64) string { return time.Unix(u, 0).UTC().Format("2 Jan 2006") },
		"add":  func(a, b int) int { return a + b },
		"commitURL": func(remote, sha string) string {
			if strings.HasPrefix(remote, "https://github.com/") || strings.HasPrefix(remote, "https://gitlab.com/") {
				return remote + "/commit/" + sha
			}
			return ""
		},
		"demo":  func() bool { return s.ReviewerKey == "" },
		"deref": func(p *int) int { return *p },
	}
	s.tpl = map[string]*template.Template{}
	for _, page := range []string{"home", "profile", "project", "session", "review", "reviews", "how"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(webFS, "web/layout.html", "web/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", page, err)
		}
		s.tpl[page] = t
	}
	return s, nil
}

// Dur formats seconds as "3h 05m" or "12m".
func Dur(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	h, m := sec/3600, (sec%3600)/60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %02dm", h, m)
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /how", s.how)
	mux.HandleFunc("GET /u/{name}", s.profile)
	mux.HandleFunc("GET /u/{name}/p/{project}", s.project)
	mux.HandleFunc("GET /u/{name}/s/{id}", s.session)
	mux.HandleFunc("GET /review", s.reviewer(s.reviews))
	mux.HandleFunc("GET /review/login", s.reviewLogin)
	mux.HandleFunc("GET /review/{id}", s.reviewer(s.review))
	mux.HandleFunc("POST /review/{id}", s.reviewer(s.reviewPost))
	mux.HandleFunc("GET /live", s.live)

	mux.HandleFunc("POST /api/v1/signup", s.apiSignup)
	mux.HandleFunc("POST /api/v1/sessions", s.auth(s.apiSessions))
	mux.HandleFunc("POST /api/v1/submissions", s.auth(s.apiSubmit))
	mux.HandleFunc("GET /api/v1/me", s.auth(func(w http.ResponseWriter, r *http.Request, u store.User) { writeJSON(w, 200, u) }))
	mux.HandleFunc("GET /api/v1/users/{name}", s.apiUser)
	mux.HandleFunc("GET /api/v1/leaderboard", s.apiLeaderboard)
	return logRequests(mux)
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		if r.URL.Path != "/live" && !strings.HasPrefix(r.URL.Path, "/static/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) render(w http.ResponseWriter, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Page"] = page
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl[page].Execute(w, data); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

func notFound(w http.ResponseWriter) { http.Error(w, "not found", http.StatusNotFound) }

// ---- auth ----

func (s *Server) auth(fn func(http.ResponseWriter, *http.Request, store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		u, err := s.Store.UserByKey(key)
		if err != nil || key == "" {
			writeJSON(w, 401, map[string]string{"error": "bad or missing API key"})
			return
		}
		fn(w, r, u)
	}
}

func (s *Server) isReviewer(r *http.Request) bool {
	if s.ReviewerKey == "" {
		return true
	}
	c, err := r.Cookie("grit_reviewer")
	return err == nil && c.Value == s.ReviewerKey
}

func (s *Server) reviewer(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isReviewer(r) {
			http.Error(w, "reviewers only: visit /review/login?key=...", http.StatusForbidden)
			return
		}
		fn(w, r)
	}
}

func (s *Server) reviewLogin(w http.ResponseWriter, r *http.Request) {
	if s.ReviewerKey == "" || r.URL.Query().Get("key") != s.ReviewerKey {
		http.Error(w, "bad key", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "grit_reviewer", Value: s.ReviewerKey, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 86400})
	http.Redirect(w, r, "/review", http.StatusFound)
}

// ---- API ----

func (s *Server) apiSignup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	key, err := s.Store.CreateUser(in.Username)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, map[string]string{"username": in.Username, "key": key})
}

func (s *Server) apiSessions(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Sessions []model.Result `json:"sessions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	// Defence in depth: strip anything private even if a client sent it.
	clean := make([]model.Result, 0, len(in.Sessions))
	for _, x := range in.Sessions {
		if x.SessionID == "" {
			continue
		}
		clean = append(clean, x.Public())
	}
	if err := s.Store.UpsertSessions(u.ID, clean); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	for _, x := range clean {
		s.hub.publish(liveEvent{User: u.Username, Project: x.Project, Harness: x.Harness, Credited: x.CreditedSec, Session: x.SessionID})
	}
	writeJSON(w, 200, map[string]int{"stored": len(clean)})
}

func (s *Server) apiSubmit(w http.ResponseWriter, r *http.Request, u store.User) {
	var b store.Bundle
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if b.Project == "" || len(b.Sessions) == 0 {
		writeJSON(w, 400, map[string]string{"error": "project and sessions required"})
		return
	}
	// Submitted sessions are also logged sessions.
	pub := make([]model.Result, len(b.Sessions))
	for i, x := range b.Sessions {
		pub[i] = x.Public()
	}
	if err := s.Store.UpsertSessions(u.ID, pub); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, err := s.Store.CreateSubmission(u.ID, b)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	s.hub.publish(liveEvent{User: u.Username, Project: b.Project, Kind: "submission"})
	writeJSON(w, 201, map[string]any{"id": id, "review_path": fmt.Sprintf("/review/%d", id)})
}

func (s *Server) apiUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserByName(r.PathValue("name"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "no such user"})
		return
	}
	p, err := s.Store.Profile(u)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) apiLeaderboard(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Leaderboard(time.Now().Add(-7 * 24 * time.Hour))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	t, _ := s.Store.SiteTotals()
	writeJSON(w, 200, map[string]any{"rows": rows, "totals": t})
}

// ---- pages ----

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.Store.Leaderboard(time.Now().Add(-7 * 24 * time.Hour))
	t, _ := s.Store.SiteTotals()
	s.render(w, "home", map[string]any{"Rows": rows, "Totals": t, "Title": "grit"})
}

func (s *Server) how(w http.ResponseWriter, r *http.Request) {
	s.render(w, "how", map[string]any{"Title": "How time is counted"})
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserByName(r.PathValue("name"))
	if err != nil {
		notFound(w)
		return
	}
	p, err := s.Store.Profile(u)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "profile", map[string]any{"Title": u.Username, "Profile": p, "User": u})
}

func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserByName(r.PathValue("name"))
	if err != nil {
		notFound(w)
		return
	}
	pr, ss, cs, err := s.Store.ProjectSessions(u, r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "project", map[string]any{"Title": pr.Name, "User": u, "Project": pr, "Sessions": ss, "Commits": cs})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UserByName(r.PathValue("name"))
	if err != nil {
		notFound(w)
		return
	}
	res, err := s.Store.Session(u, r.PathValue("id"))
	if err != nil {
		notFound(w)
		return
	}
	s.render(w, "session", map[string]any{"Title": "Session", "User": u, "S": res})
}

func (s *Server) reviews(w http.ResponseWriter, r *http.Request) {
	subs, err := s.Store.Submissions(`1 = 1`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "reviews", map[string]any{"Title": "Review queue", "Subs": subs})
}

// sessionCheck is what the server itself works out about a submitted
// session, independent of anything the client claimed.
type sessionCheck struct {
	Attest string              // witnessed, backfilled, mismatch, or "" when the session has no attestation
	Links  []attest.LinkResult // per turn, by index
	Tier   string
}

// LinkStatus is the attestation status of turn i, for templates.
func (c sessionCheck) LinkStatus(i int) string {
	if i < len(c.Links) {
		return string(c.Links[i].Status)
	}
	return ""
}

func (s *Server) check(userID int64, x model.Result) sessionCheck {
	var c sessionCheck
	seen, _ := s.Store.SeenLinks(userID, x.SessionID)
	if len(x.Turns) > 0 && (x.Salt != "" || len(seen) > 0) {
		salt, _ := hex.DecodeString(x.Salt)
		st, links := attest.Verify(x.SessionID, salt, x.Turns, func(i int) []attest.Seen { return seen[i] }, attest.Window)
		c.Attest, c.Links = string(st), links
	}
	c.Tier = model.Tier(c.Attest == string(attest.Witnessed), x.Signal == "activity")
	if c.Attest == string(attest.Mismatch) {
		c.Tier = model.TierLow
	}
	return c
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sub, b, err := s.Store.Submission(id)
	if err != nil {
		notFound(w)
		return
	}
	sort.Slice(b.Sessions, func(i, j int) bool { return b.Sessions[i].Start.Before(b.Sessions[j].Start) })
	var agent, overlap, prompts, low, debug, commits int
	flagged := 0
	checks := make([]sessionCheck, len(b.Sessions))
	attestCount := map[string]int{}
	tierCount := map[string]int{}
	owner, _ := s.Store.UserByName(sub.Username)
	for i, x := range b.Sessions {
		checks[i] = s.check(owner.ID, x)
		attestCount[checks[i].Attest]++
		tierCount[checks[i].Tier]++
		agent += x.AgentSec
		overlap += x.OverlapSec
		prompts += x.Prompts
		low += x.LowEffortPrompts
		debug += x.DebugPrompts
		commits += len(x.Commits)
		if len(x.Flags) > 0 {
			flagged++
		}
	}
	s.render(w, "review", map[string]any{
		"Title": fmt.Sprintf("Review #%d", id), "Sub": sub, "B": b,
		"Agent": agent, "Overlap": overlap, "Prompts": prompts, "Low": low, "Debug": debug,
		"CommitCount": commits, "Flagged": flagged,
		"Checks": checks, "AttestCount": attestCount, "TierCount": tierCount,
	})
}

func (s *Server) reviewPost(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	status := r.FormValue("status")
	if status != "approved" && status != "deflated" && status != "rejected" {
		http.Error(w, "bad status", 400)
		return
	}
	hrs, _ := strconv.ParseFloat(r.FormValue("approved_hours"), 64)
	if status == "rejected" {
		hrs = 0
	}
	if err := s.Store.Review(id, status, int(hrs*3600), r.FormValue("note")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/review/%d", id), http.StatusSeeOther)
}

// ---- live updates (server-sent events) ----

type liveEvent struct {
	User     string `json:"user"`
	Project  string `json:"project"`
	Harness  string `json:"harness,omitempty"`
	Credited int    `json:"credited,omitempty"`
	Session  string `json:"session,omitempty"`
	Kind     string `json:"kind,omitempty"`
	At       int64  `json:"at"`
}

type hub struct {
	mu   sync.Mutex
	subs map[chan liveEvent]string
}

func newHub() *hub { return &hub{subs: map[chan liveEvent]string{}} }

func (h *hub) publish(e liveEvent) {
	e.At = time.Now().Unix()
	if e.Kind == "" {
		e.Kind = "session"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, filter := range h.subs {
		if filter != "" && !strings.EqualFold(filter, e.User) {
			continue
		}
		select {
		case ch <- e:
		default: // slow client, drop
		}
	}
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan liveEvent, 16)
	s.hub.mu.Lock()
	s.hub.subs[ch] = r.URL.Query().Get("u")
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, ch)
		s.hub.mu.Unlock()
	}()
	fmt.Fprint(w, ": hello\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}
