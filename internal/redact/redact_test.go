package redact

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	in := "use sk-ant-abcdefghijklmnop1234 and ghp_abcdefghijklmnopqrstuvwxyz12 email me at kid@example.com, password: hunter2 postgres://u:pw@db"
	out := Text(in)
	for _, leak := range []string{"sk-ant-abc", "ghp_abc", "kid@example.com", "hunter2", "u:pw@"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in %q", leak, out)
		}
	}
	if !strings.Contains(out, "use [api-key]") {
		t.Errorf("unexpected %q", out)
	}
}
