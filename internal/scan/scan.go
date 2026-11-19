// Package scan runs every collector, links sessions to git, scores them and
// removes double counting. The client calls it on every sync.
package scan

import (
	"os"
	"time"

	"github.com/n1k0s1/grit/internal/activity"
	"github.com/n1k0s1/grit/internal/collect"
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
	parsed  map[string]fileKey
	cache   map[string][]model.Session
	commits map[string][]model.Commit
}

// New makes a scanner for a home directory.
func New(home string) *Scanner {
	return &Scanner{Home: home, Config: score.Default, Activity: activity.Log{Dir: activity.DefaultDir(home)}, parsed: map[string]fileKey{}, cache: map[string][]model.Session{}, commits: map[string][]model.Commit{}}
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
	for _, sess := range sessions {
		if sess.Cwd != "" {
			repo := gitinfo.Resolve(sess.Cwd)
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
	score.Dedupe(out)
	return out, nil
}
