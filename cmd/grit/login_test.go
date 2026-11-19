package main

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/n1k0s1/grit/internal/server"
	"github.com/n1k0s1/grit/internal/store"
)

func post(h http.Handler, host string, form url.Values) int {
	r := httptest.NewRequest("POST", "/callback", strings.NewReader(form.Encode()))
	r.Host = host
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestCallback(t *testing.T) {
	state := newState()
	cb := newCallback(state, 4321)
	good := url.Values{"state": {state}, "key": {"grit_abc"}, "username": {"ava"}}
	bad := url.Values{"state": {newState()}, "key": {"grit_abc"}, "username": {"ava"}}

	if code := post(cb, "evil.example:4321", good); code != http.StatusForbidden {
		t.Fatalf("other host: %d", code)
	}
	if code := post(cb, "127.0.0.1:4321", bad); code != http.StatusBadRequest {
		t.Fatalf("wrong state: %d", code)
	}
	if code := post(cb, "127.0.0.1:4321", url.Values{"state": {state}}); code != http.StatusBadRequest {
		t.Fatalf("no key: %d", code)
	}
	r := httptest.NewRequest("GET", "/callback?state="+state, nil)
	r.Host = "127.0.0.1:4321"
	w := httptest.NewRecorder()
	cb.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET: %d", w.Code)
	}
	if code := post(cb, "127.0.0.1:4321", good); code != http.StatusOK {
		t.Fatalf("good: %d", code)
	}
	if res := <-cb.done; res.Key != "grit_abc" || res.Username != "ava" {
		t.Fatalf("%+v", res)
	}
	if code := post(cb, "127.0.0.1:4321", good); code != http.StatusGone {
		t.Fatalf("second use: %d", code)
	}
}

var (
	actionRe = regexp.MustCompile(`action="([^"]+)"`)
	hiddenRe = regexp.MustCompile(`name="(\w+)" value="([^"]*)"`)
)

// TestBrowserLoginEndToEnd plays the browser against the real server
// handler: open the link, pick a username, follow the auto-submitted form.
func TestBrowserLoginEndToEnd(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "grit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := server.New(st, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	browser := func(link string) error {
		go func() {
			u, _ := url.Parse(link)
			q := u.Query()
			// The page the user sees first.
			if resp, err := http.Get(link); err != nil || resp.StatusCode != 200 {
				t.Errorf("open link: %v", err)
				return
			}
			resp, err := http.PostForm(ts.URL+"/cli-auth", url.Values{"port": {q.Get("port")}, "state": {q.Get("state")}, "username": {"ava"}})
			if err != nil {
				t.Errorf("submit: %v", err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.Header.Get("X-Frame-Options") != "DENY" {
				t.Errorf("handoff page can be framed")
			}
			m := actionRe.FindSubmatch(body)
			if m == nil || !strings.HasPrefix(string(m[1]), "http://127.0.0.1:"+q.Get("port")+"/") {
				t.Errorf("handoff must post to the loopback port, got %s", body)
				return
			}
			form := url.Values{}
			for _, h := range hiddenRe.FindAllSubmatch(body, -1) {
				form.Set(string(h[1]), html.UnescapeString(string(h[2])))
			}
			r, err := http.PostForm(html.UnescapeString(string(m[1])), form)
			if err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}
	res, err := browserLogin(ts.URL, browser, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByKey(res.Key)
	if err != nil || u.Username != "ava" || res.Username != "ava" {
		t.Fatalf("key does not belong to ava: %v %+v", err, res)
	}
}

func TestCLIAuthRejectsBadParams(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "grit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, _ := server.New(st, "")
	for _, q := range []string{"port=80&state=" + newState(), "port=5000&state=short", "port=abc&state=" + newState()} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/cli-auth?"+q, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
}
