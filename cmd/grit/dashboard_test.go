package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

func testDash(t *testing.T) *dash {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GRIT_HOME", home)
	now := time.Now()
	rs := []*model.Result{{
		SessionID: "s1", Harness: "claude-code", Project: "game", Start: now.Add(-time.Hour), End: now.Add(-50 * time.Minute),
		CreditedSec: 600, WallSec: 700, Prompts: 1, Signal: "activity",
		Turns:     []model.Turn{{PromptAt: now.Add(-time.Hour), PromptText: "add a pause menu, key is sk-live-abcdefghijklmnopqrstuvwx", ThinkRule: "t", AgentRule: "a"}},
		Intervals: []model.Interval{{Start: now.Add(-time.Hour), End: now.Add(-50 * time.Minute), Kind: "think", Rule: "r"}},
	}}
	d, err := newDash(home, func() []*model.Result { return rs })
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var routes = []struct{ method, path string }{
	{"GET", "/"}, {"GET", "/sessions"}, {"GET", "/s/s1"}, {"GET", "/week"}, {"GET", "/privacy"},
	{"GET", "/submit"}, {"GET", "/submit/game"}, {"GET", "/static/app.css"},
	{"POST", "/privacy/pause"}, {"POST", "/privacy/resume"}, {"POST", "/privacy/exclude"},
	{"POST", "/privacy/include"}, {"POST", "/goal"}, {"POST", "/submit/game"},
}

func do(h http.Handler, method, path, host string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDashboardListensOnLoopbackOnly(t *testing.T) {
	d := testDash(t)
	u, err := d.serve()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "http://127.0.0.1:") {
		t.Fatalf("dashboard must bind 127.0.0.1, got %s", u)
	}
	// Reachable on loopback, but only with the token.
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
}

func TestDashboardRefusesEveryRequestWithoutToken(t *testing.T) {
	d := testDash(t)
	d.host = "127.0.0.1:5555"
	h := d.handler()
	for _, rt := range routes {
		if w := do(h, rt.method, rt.path, d.host, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s %s without token: %d", rt.method, rt.path, w.Code)
		}
		if w := do(h, rt.method, rt.path, d.host, func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: "wrong"})
		}); w.Code != http.StatusForbidden {
			t.Errorf("%s %s with a wrong cookie: %d", rt.method, rt.path, w.Code)
		}
		if w := do(h, rt.method, rt.path, "evil.example:5555", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: d.token})
		}); w.Code != http.StatusForbidden {
			t.Errorf("%s %s with a rebound host name: %d", rt.method, rt.path, w.Code)
		}
	}
}

func TestDashboardWithToken(t *testing.T) {
	d := testDash(t)
	d.host = "127.0.0.1:5555"
	h := d.handler()

	w := do(h, "GET", "/week?token="+d.token, d.host, nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/week" {
		t.Fatalf("token swap: %d %q", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", c)
	}
	auth := func(r *http.Request) { r.AddCookie(c[0]) }
	for _, rt := range routes {
		if rt.method != "GET" {
			continue
		}
		w := do(h, "GET", rt.path, d.host, auth)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: %d %s", rt.path, w.Code, w.Body)
		}
	}
	// The replay shows the person's own prompt; the submit preview shows it redacted.
	if body := do(h, "GET", "/s/s1", d.host, auth).Body.String(); !strings.Contains(body, "sk-live-abcdefghijklmnopqrstuvwx") {
		t.Error("replay should show the full local prompt")
	}
	if body := do(h, "GET", "/submit/game", d.host, auth).Body.String(); strings.Contains(body, "sk-live-abcdefghijklmnopqrstuvwx") {
		t.Error("submit preview must show the redacted prompt")
	}

	form := func(r *http.Request) {
		auth(r)
		r.Body = io.NopCloser(strings.NewReader(url.Values{"weekly_hours": {"12"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if w := do(h, "POST", "/goal", d.host, form); w.Code != http.StatusForbidden {
		t.Fatalf("POST without Origin: %d", w.Code)
	}
	if w := do(h, "POST", "/goal", d.host, func(r *http.Request) { form(r); r.Header.Set("Origin", "http://evil.example") }); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: %d", w.Code)
	}
	if w := do(h, "POST", "/goal", d.host, func(r *http.Request) { form(r); r.Header.Set("Origin", "http://"+d.host) }); w.Code != http.StatusSeeOther {
		t.Fatalf("same-origin POST: %d %s", w.Code, w.Body)
	}
	if loadGoal().WeeklyHours != 12 {
		t.Fatal("goal not saved")
	}
	if w := do(h, "POST", "/privacy/pause", d.host, func(r *http.Request) { auth(r); r.Header.Set("Origin", "http://"+d.host) }); w.Code != http.StatusSeeOther {
		t.Fatalf("pause: %d", w.Code)
	}
	if body := do(h, "GET", "/", d.host, auth).Body.String(); !strings.Contains(body, "grit is paused") {
		t.Fatal("paused banner missing")
	}
}
