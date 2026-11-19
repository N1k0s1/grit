// Package edits finds code written by hand. It polls the working trees of
// recently active projects for changed files, then decides who made each
// change: an agent (a run or a tool call on that file was happening), a
// person (no agent nearby and keyboard or mouse input that minute), or
// unknown (formatters, git checkouts, builds, or no activity data).
//
// Polling file modification times is used instead of OS file events. On
// macOS the only cgo-free event API is kqueue, which needs an open file
// descriptor for every watched file and falls over on a large repo. A poll
// every 30 seconds costs one stat per file and 16 bytes of state per file,
// and works the same on every OS. The modification time gives the exact
// moment of the last write, so polling loses only repeated writes to the
// same file between two polls.
package edits

import (
	"hash/fnv"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// Change is one file written on disk.
type Change struct {
	At   time.Time `json:"at"`
	Root string    `json:"root"` // repository root
	Path string    `json:"path"` // slash path relative to Root
}

// DefaultMaxFiles is the largest tree the watcher will track. Bigger roots
// are dropped rather than slowing an old laptop down.
const DefaultMaxFiles = 100000

// skipDirs are never walked: version control, dependencies and build output.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "out": true,
	"target": true, "bin": true, "obj": true, "venv": true, "env": true,
	"__pycache__": true, "coverage": true, "Pods": true, "DerivedData": true,
}

// SkipDir reports whether a directory name is ignored. Hidden directories
// (.git, .next, .venv, .cache, ...) are always ignored.
func SkipDir(name string) bool { return strings.HasPrefix(name, ".") || skipDirs[name] }

// Watcher polls a set of roots. It is not safe for concurrent use.
type Watcher struct {
	MaxFiles int // per root; defaults to DefaultMaxFiles

	roots  map[string]map[uint64]int64 // root -> hash of relative path -> mtime (unix nanos)
	tooBig map[string]bool
}

// SetRoots replaces the watched roots, keeping state for roots that stay.
func (w *Watcher) SetRoots(roots []string) {
	next := map[string]map[uint64]int64{}
	for _, r := range roots {
		if w.tooBig[r] {
			continue
		}
		next[r] = w.roots[r] // nil for a new root: its first poll is a baseline
	}
	w.roots = next
}

// Files is the number of files currently tracked.
func (w *Watcher) Files() int {
	n := 0
	for _, m := range w.roots {
		n += len(m)
	}
	return n
}

func hashPath(p string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p))
	return h.Sum64()
}

// Poll walks every root and returns files written since the last poll. The
// first poll of a root only records a baseline.
func (w *Watcher) Poll() []Change {
	if w.MaxFiles == 0 {
		w.MaxFiles = DefaultMaxFiles
	}
	if w.tooBig == nil {
		w.tooBig = map[string]bool{}
	}
	var out []Change
	for root, old := range w.roots {
		baseline := old == nil
		next := make(map[uint64]int64, len(old))
		var found []Change
		over := false
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			if d.IsDir() {
				if p != root && SkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if len(next) >= w.MaxFiles {
				over = true
				return filepath.SkipAll
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			k, mt := hashPath(rel), info.ModTime().UnixNano()
			next[k] = mt
			if prev, ok := old[k]; !baseline && (!ok || prev != mt) {
				found = append(found, Change{At: info.ModTime().UTC(), Root: root, Path: rel})
			}
			return nil
		})
		if over {
			w.tooBig[root] = true
			delete(w.roots, root)
			continue
		}
		w.roots[root] = next
		out = append(out, found...)
	}
	return out
}
