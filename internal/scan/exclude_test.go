package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExcluded(t *testing.T) {
	home := filepath.FromSlash("/home/sam")
	pats := []string{"~/private", filepath.FromSlash("/work/client-*"), filepath.FromSlash("/exact/repo/")}
	cases := []struct {
		path string
		want bool
	}{
		{"/home/sam/private", true},
		{"/home/sam/private/notes/app", true},
		{"/home/sam/private-but-not", false},
		{"/work/client-acme", true},
		{"/work/client-acme/src", true},
		{"/work/clients", false},
		{"/exact/repo", true},
		{"/exact/repository", false},
		{"/home/sam/game", false},
		{"", false},
	}
	for _, c := range cases {
		p := filepath.FromSlash(c.path)
		if got := Excluded(ExpandHome(pats, home), p); got != c.want {
			t.Errorf("%q: got %v want %v", c.path, got, c.want)
		}
	}
}

func TestExcludeFile(t *testing.T) {
	home := t.TempDir()
	if err := AddExclude(home, "~/secret"); err != nil {
		t.Fatal(err)
	}
	if err := AddExclude(home, "~/secret"); err != nil { // no duplicate
		t.Fatal(err)
	}
	if err := AddExclude(home, "/tmp/other"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(ExcludePath(home))
	got := LoadExclude(home)
	if len(got) != 2 || got[0] != filepath.Join(home, "secret") {
		t.Fatalf("got %v from %q", got, b)
	}
	if err := RemoveExclude(home, "~/secret"); err != nil {
		t.Fatal(err)
	}
	if got := LoadExclude(home); len(got) != 1 {
		t.Fatalf("after remove: %v", got)
	}
}
