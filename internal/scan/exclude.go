package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// The exclude list keeps folders out of grit completely: their sessions are
// not scored or synced, git is never run in them, and their files are never
// walked. It lives in ~/.grit/exclude, one folder per line. Lines starting
// with # are comments, ~ means the home folder, and the last part of a line
// may use * and ? (for example ~/work/client-*).

// ExcludePath is the exclude file for a home directory.
func ExcludePath(home string) string { return filepath.Join(home, ".grit", "exclude") }

func readLines(home string) []string {
	f, err := os.Open(ExcludePath(home))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// ExpandHome turns ~ into the home folder and cleans each pattern.
func ExpandHome(pats []string, home string) []string {
	out := make([]string, 0, len(pats))
	for _, p := range pats {
		if p == "~" {
			p = home
		} else if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
			p = filepath.Join(home, p[2:])
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// LoadExclude reads the exclude list with ~ expanded.
func LoadExclude(home string) []string { return ExpandHome(readLines(home), home) }

// Excluded reports whether path is inside an excluded folder.
func Excluded(pats []string, path string) bool {
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	for _, p := range pats {
		if path == p || strings.HasPrefix(path, p+string(filepath.Separator)) {
			return true
		}
		if !strings.ContainsAny(p, "*?[") {
			continue
		}
		for a := path; ; a = filepath.Dir(a) {
			if ok, _ := filepath.Match(p, a); ok {
				return true
			}
			if filepath.Dir(a) == a {
				break
			}
		}
	}
	return false
}

func writeLines(home string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(ExcludePath(home)), 0o700); err != nil {
		return err
	}
	body := "# Folders grit never reads, walks or syncs. One per line; ~ is your home folder.\n"
	for _, l := range lines {
		body += l + "\n"
	}
	return os.WriteFile(ExcludePath(home), []byte(body), 0o600)
}

// AddExclude adds a folder or pattern to the list (no duplicates).
func AddExclude(home, pat string) error {
	lines := readLines(home)
	for _, l := range lines {
		if l == pat {
			return nil
		}
	}
	return writeLines(home, append(lines, pat))
}

// RemoveExclude removes an exact line from the list.
func RemoveExclude(home, pat string) error {
	var keep []string
	for _, l := range readLines(home) {
		if l != pat {
			keep = append(keep, l)
		}
	}
	return writeLines(home, keep)
}
