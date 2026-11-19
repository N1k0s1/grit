// Package autostart starts the grit daemon at login, written by hand per
// OS with no autostart library: a LaunchAgent on macOS, an XDG autostart
// entry on Linux and a Run registry value on Windows. Everything it creates
// is removed by Uninstall.
package autostart

import (
	"os/exec"
)

// Label names the login item on every OS.
const Label = "com.github.n1k0s1.grit"

// Spec is what to start at login.
type Spec struct {
	Home string   // the user's home folder
	Exe  string   // absolute path of the grit binary to run
	Args []string // usually ["daemon"]
	Log  string   // file for the daemon's output, where the OS supports it
}

// Run executes an OS command. Tests replace it so nothing touches the
// real login items.
var Run = func(name string, args ...string) error { return exec.Command(name, args...).Run() }

// Install registers the login item and starts it where the OS allows. It
// returns a sentence saying what it did.
func Install(sp Spec) (string, error) { return install(sp) }

// Uninstall removes the login item and stops it. It returns what it removed.
func Uninstall(home string) (string, error) { return uninstall(home) }

// Installed reports whether the login item exists, and where.
func Installed(home string) (bool, string) { return installed(home) }
