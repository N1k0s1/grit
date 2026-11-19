// Package gitinfo links sessions to repositories and commits using the local
// git binary. Everything here is best effort: no git, no repo, no problem.
package gitinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

// Repo describes where a session's working directory lives.
type Repo struct {
	Root    string
	Name    string
	Remote  string // normalised to https when it is a known host
	Present bool
}

var (
	mu    sync.Mutex
	cache = map[string]Repo{}
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

var sshRemote = regexp.MustCompile(`^git@([^:]+):(.+?)(\.git)?$`)

// NormaliseRemote turns git@github.com:a/b.git into https://github.com/a/b.
func NormaliseRemote(u string) string {
	u = strings.TrimSpace(u)
	if m := sshRemote.FindStringSubmatch(u); m != nil {
		return "https://" + m[1] + "/" + m[2]
	}
	u = strings.TrimSuffix(u, ".git")
	if i := strings.Index(u, "@"); strings.HasPrefix(u, "https://") && i > 0 {
		u = "https://" + u[i+1:] // drop embedded credentials
	}
	return u
}

// Resolve finds the repo for a working directory.
func Resolve(cwd string) Repo {
	mu.Lock()
	if r, ok := cache[cwd]; ok {
		mu.Unlock()
		return r
	}
	mu.Unlock()
	r := Repo{Root: cwd, Name: filepath.Base(cwd)}
	if st, err := os.Stat(cwd); err == nil && st.IsDir() {
		r.Present = true
		if top, err := git(cwd, "rev-parse", "--show-toplevel"); err == nil && top != "" {
			r.Root = filepath.Clean(top) // git prints forward slashes, even on Windows
			r.Name = filepath.Base(r.Root)
			if rem, err := git(r.Root, "remote", "get-url", "origin"); err == nil {
				r.Remote = NormaliseRemote(rem)
				if base := filepath.Base(r.Remote); base != "" && base != "." {
					r.Name = base
				}
			}
		}
	}
	mu.Lock()
	cache[cwd] = r
	mu.Unlock()
	return r
}

// Commits lists commits in the repo between two times (with a little slack
// either side, since people commit right after the agent finishes).
func Commits(root string, from, to time.Time) []model.Commit {
	out, err := git(root, "log", "--all", "--no-merges",
		"--since="+from.Add(-5*time.Minute).Format(time.RFC3339),
		"--until="+to.Add(30*time.Minute).Format(time.RFC3339),
		"--format=@@%H%x1f%ct%x1f%s", "--shortstat")
	if err != nil || out == "" {
		return nil
	}
	var cs []model.Commit
	for _, block := range strings.Split(out, "@@")[1:] {
		lines := strings.SplitN(strings.TrimSpace(block), "\n", 2)
		f := strings.Split(lines[0], "\x1f")
		if len(f) != 3 {
			continue
		}
		ts, _ := strconv.ParseInt(f[1], 10, 64)
		c := model.Commit{SHA: f[0], At: time.Unix(ts, 0).UTC(), Subject: f[2]}
		if len(lines) == 2 {
			c.Additions = statNum(lines[1], "insertion")
			c.Deletions = statNum(lines[1], "deletion")
		}
		cs = append(cs, c)
	}
	return cs
}

func statNum(s, word string) int {
	re := regexp.MustCompile(`(\d+) ` + word)
	if m := re.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}
