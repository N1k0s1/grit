//go:build darwin

package autostart

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLaunchAgent(t *testing.T) {
	var calls []string
	Run = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	defer func() { Run = func(name string, args ...string) error { return exec.Command(name, args...).Run() } }()

	home := t.TempDir()
	sp := Spec{Home: home, Exe: home + "/.grit/bin/grit & co", Args: []string{"daemon"}, Log: home + "/.grit/daemon.log"}
	if _, err := Install(sp); err != nil {
		t.Fatal(err)
	}
	ok, p := Installed(home)
	if !ok {
		t.Fatal("not installed")
	}
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plist does not lint: %s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "<string>"+home+"/.grit/bin/grit &amp; co</string>") {
		t.Fatalf("binary path not escaped in plist:\n%s", b)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "launchctl bootstrap gui/") {
		t.Fatalf("not loaded: %v", calls)
	}
	if _, err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Installed(home); ok {
		t.Fatal("plist left behind")
	}
	if !strings.Contains(calls[len(calls)-1], "launchctl bootout gui/") {
		t.Fatalf("not stopped: %v", calls)
	}
}
