package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/attest"
	"github.com/n1k0s1/grit/internal/model"
	"github.com/n1k0s1/grit/internal/store"
)

var t0 = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)

type harness struct {
	t   *testing.T
	st  *store.Store
	srv *httptest.Server
	key string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "grit.db"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(st, "")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, st: st, srv: httptest.NewServer(s.Handler())}
	t.Cleanup(h.srv.Close)
	var out struct{ Key string }
	h.do("POST", "/api/v1/signup", map[string]string{"username": "ava"}, &out)
	h.key = out.Key
	return h
}

func (h *harness) do(method, path string, body, out any) string {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if h.key != "" {
		req.Header.Set("Authorization", "Bearer "+h.key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		h.t.Fatalf("%s %s: %s %s", method, path, resp.Status, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			h.t.Fatal(err)
		}
	}
	return string(b)
}

// clientSession is a scored session as the client holds it: redacted
// prompts, attestation links and salt.
func clientSession(id, signal string) model.Result {
	r := model.Result{SessionID: id, Harness: "claude-code", Project: "game", Start: t0, End: t0.Add(8 * time.Minute), Signal: signal, Prompts: 2, CreditedSec: 300, WallSec: 480}
	r.Turns = []model.Turn{
		{Index: 0, PromptAt: t0, PromptText: "add a pause menu", ThinkRule: "first prompt", AgentRule: "supervised"},
		{Index: 1, PromptAt: t0.Add(4 * time.Minute), PromptText: "now map it to the start button", ThinkRule: "replied", AgentRule: "last run"},
	}
	salt := attest.Salt([]byte("client device secret"), id)
	for i, l := range attest.Chain(id, salt, r.Turns) {
		r.Turns[i].Link = l
	}
	r.Salt = hex.EncodeToString(salt)
	return r
}

func TestSubmissionAttestationAndTier(t *testing.T) {
	h := newHarness(t)
	honest := clientSession("s1", "activity")
	tampered := clientSession("s2", "activity")

	// Both sessions sync live, a minute after they end.
	h.st.Now = func() time.Time { return t0.Add(9 * time.Minute) }
	h.do("POST", "/api/v1/sessions", map[string]any{"sessions": []model.Result{honest.Public(), tampered.Public()}}, nil)

	// Profile shows the tier for both.
	prof := h.do("GET", "/api/v1/users/ava", nil, nil)
	if strings.Count(prof, `"tier":"high"`) != 2 {
		t.Fatalf("both live sessions with activity should be high: %s", prof)
	}

	if page := h.do("GET", "/u/ava/s/s1", nil, nil); !strings.Contains(page, `<span class="tier high"`) || !strings.Contains(page, `<span class="att witnessed"`) {
		t.Fatalf("session page should show the high tier and the witness")
	}
	if page := h.do("GET", "/u/ava", nil, nil); !strings.Contains(page, `"tier":"high"`) {
		t.Fatalf("profile page data should carry each session's tier")
	}

	// A day later the second session is submitted with a rewritten prompt.
	h.st.Now = func() time.Time { return t0.Add(24 * time.Hour) }
	tampered.Turns[1].PromptText = "now map it to the start button and also write the whole game"
	var out struct{ ID int64 }
	h.do("POST", "/api/v1/submissions", store.Bundle{Project: "game", Sessions: []model.Result{honest, tampered}}, &out)

	page := h.do("GET", "/review/"+itoa(out.ID), nil, nil)
	for _, want := range []string{
		`<b>1</b> witnessed live`,
		`<b>1</b> failed`,
		`<span class="tier high"`,
		`<span class="tier low"`,
		`<span class="att mismatch"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("review page missing %q", want)
		}
	}
}

func TestServerRecomputesAnomalies(t *testing.T) {
	h := newHarness(t)
	// A client that reports no flags on a templated session.
	r := clientSession("s3", "")
	r.Turns = nil
	for i := 0; i < 6; i++ {
		r.Turns = append(r.Turns, model.Turn{Index: i, PromptAt: t0.Add(time.Duration(i*5) * time.Minute),
			PromptText: "Improve the game. Iteration " + itoa(int64(i)) + ".", ThinkRule: "x", AgentRule: "y"})
	}
	r.Flags = nil
	var out struct{ ID int64 }
	h.do("POST", "/api/v1/submissions", store.Bundle{Project: "game", Sessions: []model.Result{r}}, &out)
	page := h.do("GET", "/review/"+itoa(out.ID), nil, nil)
	for _, want := range []string{"regular-timing (server)", "repeated-prompts (server)"} {
		if !strings.Contains(page, want) {
			t.Errorf("review page missing %q", want)
		}
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
