//go:build !darwin && !windows

package autostart

import (
	"os"
	"strings"
	"testing"
)

func TestDesktopEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	sp := Spec{Home: home, Exe: home + "/my apps/grit", Args: []string{"daemon"}}
	if _, err := Install(sp); err != nil {
		t.Fatal(err)
	}
	ok, p := Installed(home)
	if !ok {
		t.Fatal("not installed")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `Exec="`+home+`/my apps/grit" daemon`) {
		t.Fatalf("bad Exec line:\n%s", b)
	}
	if _, err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Installed(home); ok {
		t.Fatal("entry left behind")
	}
}
