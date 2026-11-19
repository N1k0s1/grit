//go:build darwin

package autostart

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func plistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Plist is the LaunchAgent for a spec. KeepAlive restarts the daemon if it
// crashes but not after a clean exit, and ProcessType Background tells
// macOS it is low priority.
func Plist(sp Spec) string {
	var args bytes.Buffer
	for _, a := range append([]string{sp.Exe}, sp.Args...) {
		fmt.Fprintf(&args, "\n\t\t<string>%s</string>", esc(a))
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>` + args.String() + `
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>` + esc(sp.Log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(sp.Log) + `</string>
</dict>
</plist>
`
}

func domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func install(sp Spec) (string, error) {
	p := plistPath(sp.Home)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	// Replace a previous install cleanly.
	_ = Run("launchctl", "bootout", domain()+"/"+Label)
	if err := os.WriteFile(p, []byte(Plist(sp)), 0o644); err != nil {
		return "", err
	}
	if err := Run("launchctl", "bootstrap", domain(), p); err != nil {
		// Older macOS versions only know load.
		if err2 := Run("launchctl", "load", "-w", p); err2 != nil {
			return "", fmt.Errorf("wrote %s but launchctl could not load it: %v", p, err)
		}
	}
	return "Added a LaunchAgent at " + p + " and started it. It runs at every login.", nil
}

func uninstall(home string) (string, error) {
	p := plistPath(home)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return "No LaunchAgent was installed.", nil
	}
	if err := Run("launchctl", "bootout", domain()+"/"+Label); err != nil {
		_ = Run("launchctl", "unload", "-w", p)
	}
	if err := os.Remove(p); err != nil {
		return "", err
	}
	return "Stopped and removed the LaunchAgent " + p + ".", nil
}

func installed(home string) (bool, string) {
	p := plistPath(home)
	_, err := os.Stat(p)
	return err == nil, p
}
