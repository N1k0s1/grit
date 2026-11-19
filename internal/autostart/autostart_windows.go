//go:build windows

package autostart

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// RunKey is the per-user Run key. Tests point it somewhere harmless.
var RunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

const valueName = "grit"

func quote(a string) string {
	if a == "" || strings.ContainsAny(a, " \t\"") {
		return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	return a
}

// Command is the Run value for a spec.
func Command(sp Spec) string {
	parts := []string{quote(sp.Exe)}
	for _, a := range sp.Args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func install(sp Spec) (string, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, RunKey, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	if err := k.SetStringValue(valueName, Command(sp)); err != nil {
		return "", err
	}
	return `Added "grit" to HKEY_CURRENT_USER\` + RunKey + `. It runs at every login.`, nil
}

func uninstall(home string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, RunKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "No login entry was installed.", nil
	}
	if err != nil {
		return "", err
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); errors.Is(err, registry.ErrNotExist) {
		return "No login entry was installed.", nil
	} else if err != nil {
		return "", err
	}
	return `Removed "grit" from HKEY_CURRENT_USER\` + RunKey + `.`, nil
}

func installed(home string) (bool, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, RunKey, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer k.Close()
	_, _, err = k.GetStringValue(valueName)
	return err == nil, `HKEY_CURRENT_USER\` + RunKey + `\` + valueName
}
