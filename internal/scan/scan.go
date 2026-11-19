// Package scan runs every collector, links sessions to git, scores them and
// removes double counting. The client calls it on every sync.
package scan

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/n1k0s1/grit/internal/activity"
	"github.com/n1k0s1/grit/internal/collect"
	"github.com/n1k0s1/grit/internal/edits"
	"github.com/n1k0s1/grit/internal/gitinfo"
	"github.com/n1k0s1/grit/internal/model"
	"github.com/n1k0s1/grit/internal/score"
)

type fileKey struct {
	path  string
	mtime int64
	size  int64
}

// Scanner caches parsed files between runs so `grit watch` only re-reads
// logs that changed. On an old laptop that is the difference between a
// background process you never notice and one you do.
type Scanner struct {
	Home     string
	Config   score.Config
	Activity activity.Log // keyboard and mouse minutes; empty Dir disables v2
	EditsDir string       // logged file changes; empty disables hand-written time
	parsed   map[string]fileKey
	cache    map[string][]model.Session
	commits  map[string][]model.Commit
	roots    []string
}

// Roots returns the git roots of projects with a session in the last week,
// as of the last Run. These are the working trees worth watching for hand
// edits.
func (s *Scanner) Roots() []string { return s.roots }

// New makes a scanner for a home directory.
func New(home string) *Scanner {
	return &Scanner{Home: home, Config: score.Default, Activity: activity.Log{Dir: activity.DefaultDir(home)}, EditsDir: edits.DefaultDir(home), parsed: map[string]fileKey{}, cache: map[string][]model.Session{}, commits: map[string][]model.Commit{}}
}

// Run returns scored, deduplicated results for sessions active since a time.
func (s *Scanner) Run(since time.Time) ([]*model.Result, error) {
	var sessions []model.Session
	for _, c := range collect.All() {
		for _, p := range c.Paths(s.Home) {
			st, err := os.Stat(p)
			if err != nil || st.ModTime().Before(since) {
				continue
			}
			k := fileKey{p, st.ModTime().UnixNano(), st.Size()}
			if s.parsed[p] != k {
				ss, err := c.Parse(p)
				if err != nil && len(ss) == 0 {
					continue
				}
				s.cache[p] = ss
				s.parsed[p] = k
			}
			sessions = append(sessions, s.cache[p]...)
		}
	}
	var act score.Activity
	if s.Activity.Dir != "" {
		act = s.Activity.Reader() // fresh per run so new samples show up
	}
	var out []*model.Result
	var runs []edits.Run
	rootSet := map[string]bool{}
	recent := time.Now().AddDate(0, 0, -7)
	for _, sess := range sessions {
		if sess.Cwd != "" {
			repo := gitinfo.Resolve(sess.Cwd)
			if isGitRoot(repo) {
				sr := sessionRuns(sess, repo.Root)
				runs = append(runs, sr...)
				if n := len(sr); n > 0 && sr[n-1].End.After(recent) {
					rootSet[repo.Root] = true
				}
			}
			sess.Project = repo.Name
			if repo.Remote != "" {
				sess.GitRemote = repo.Remote
			}
		}
		if sess.GitRemote != "" {
			sess.GitRemote = gitinfo.NormaliseRemote(sess.GitRemote)
		}
		if sess.Project == "" {
			sess.Project = "unknown"
		}
		r := score.ScoreWith(sess, s.Config, act)
		if r.Prompts == 0 || r.End.Before(since) {
			continue
		}
		if sess.Cwd != "" {
			if repo := gitinfo.Resolve(sess.Cwd); repo.Present {
				ck := r.SessionID + r.End.String()
				if _, ok := s.commits[ck]; !ok {
					s.commits[ck] = gitinfo.Commits(repo.Root, r.Start, r.End)
				}
				r.Commits = s.commits[ck]
			}
		}
		rr := r
		out = append(out, &rr)
	}
	if s.EditsDir != "" {
		changes, _ := edits.Read(s.EditsDir, since)
		hand, counts := edits.HandSessions(changes, runs, act, func(root string) string { return gitinfo.Resolve(root).Name })
		for _, r := range out {
			r.AgentEdits = counts.BySession[r.SessionID]
		}
		for i := range hand {
			if hand[i].CreditedSec > 0 {
				out = append(out, &hand[i])
			}
		}
	}
	s.roots = s.roots[:0]
	for r := range rootSet {
		s.roots = append(s.roots, r)
	}
	sort.Strings(s.roots)
	score.Dedupe(out)
	return out, nil
}

// isGitRoot is true only for a real repository, so a session started in a
// home folder never makes grit walk the whole disk.
func isGitRoot(r gitinfo.Repo) bool {
	if !r.Present || r.Root == "" {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(r.Root) == filepath.Clean(home) {
		return false
	}
	_, err := os.Stat(filepath.Join(r.Root, ".git"))
	return err == nil
}

// sessionRuns turns a session into agent runs (prompt to last agent event)
// with the files its tool calls named, relative to the repository root.
func sessionRuns(sess model.Session, root string) []edits.Run {
	ev := append([]model.Event(nil), sess.Events...)
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.Before(ev[j].At) })
	var out []edits.Run
	for _, e := range ev {
		switch e.Kind {
		case model.Prompt:
			out = append(out, edits.Run{Session: sess.ID, Root: root, Start: e.At, End: e.At})
		default:
			if len(out) == 0 {
				continue
			}
			r := &out[len(out)-1]
			if e.At.After(r.End) {
				r.End = e.At
			}
			if e.Kind == model.Tool && e.File != "" {
				f := e.File
				if !filepath.IsAbs(f) {
					f = filepath.Join(sess.Cwd, f)
				}
				if rel, err := filepath.Rel(root, f); err == nil && !strings.HasPrefix(rel, "..") {
					r.Tools = append(r.Tools, edits.Tool{At: e.At, Path: filepath.ToSlash(rel)})
				}
			}
		}
	}
	return out
}
