//go:build !darwin && !windows

package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func desktopPath(home string) string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" || !filepath.IsAbs(dir) {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "grit.desktop")
}

// quote follows the Desktop Entry spec for Exec arguments.
func quote(a string) string {
	if !strings.ContainsAny(a, " \t\"'\\$`") {
		return a
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + r.Replace(a) + `"`
}

// Desktop is the XDG autostart entry for a spec.
func Desktop(sp Spec) string {
	parts := []string{quote(sp.Exe)}
	for _, a := range sp.Args {
		parts = append(parts, quote(a))
	}
	return "[Desktop Entry]\nType=Application\nName=grit\nComment=Tracks time spent steering AI agents\n" +
		"Exec=" + strings.Join(parts, " ") + "\nTerminal=false\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n"
}

func install(sp Spec) (string, error) {
	p := desktopPath(sp.Home)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(Desktop(sp)), 0o644); err != nil {
		return "", err
	}
	return "Added an autostart entry at " + p + ". It runs at every login.", nil
}

func uninstall(home string) (string, error) {
	p := desktopPath(home)
	if err := os.Remove(p); errors.Is(err, os.ErrNotExist) {
		return "No autostart entry was installed.", nil
	} else if err != nil {
		return "", err
	}
	return "Removed the autostart entry " + p + ".", nil
}

func installed(home string) (bool, string) {
	p := desktopPath(home)
	_, err := os.Stat(p)
	return err == nil, p
}
