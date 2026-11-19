//go:build windows

package autostart

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func TestRunKey(t *testing.T) {
	old := RunKey
	RunKey = fmt.Sprintf(`Software\grit-test-%d`, time.Now().UnixNano())
	defer func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, RunKey)
		RunKey = old
	}()
	sp := Spec{Exe: `C:\Users\Sam Lee\.grit\bin\grit.exe`, Args: []string{"daemon", "-background"}}
	if _, err := Install(sp); err != nil {
		t.Fatal(err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, RunKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := k.GetStringValue("grit")
	k.Close()
	if err != nil || v != `"C:\Users\Sam Lee\.grit\bin\grit.exe" daemon -background` {
		t.Fatalf("value %q err %v", v, err)
	}
	if ok, _ := Installed(""); !ok {
		t.Fatal("not installed")
	}
	if _, err := Uninstall(""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Installed(""); ok {
		t.Fatal("value left behind")
	}
}
