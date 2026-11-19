package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// loginResult is what the browser hands back.
type loginResult struct {
	Username, Key string
}

// callback is the one-shot handler grit login serves on 127.0.0.1. It
// accepts exactly one POST carrying the right state, from a request
// addressed to 127.0.0.1 on our port (so a web page cannot reach it
// through a rebound DNS name), and ignores everything else.
type callback struct {
	state string
	host  string // "127.0.0.1:<port>"
	done  chan loginResult
	used  bool
	mu    sync.Mutex
}

func newCallback(state string, port int) *callback {
	return &callback{state: state, host: "127.0.0.1:" + strconv.Itoa(port), done: make(chan loginResult, 1)}
}

func (c *callback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/callback" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if r.Host != c.host {
		http.Error(w, "wrong host", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	got := r.PostFormValue("state")
	if subtle.ConstantTimeCompare([]byte(got), []byte(c.state)) != 1 {
		http.Error(w, "this login link is not the one grit is waiting for", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	if c.used {
		c.mu.Unlock()
		http.Error(w, "already used", http.StatusGone)
		return
	}
	key, user := r.PostFormValue("key"), r.PostFormValue("username")
	if !strings.HasPrefix(key, "grit_") {
		c.mu.Unlock()
		http.Error(w, "no key", http.StatusBadRequest)
		return
	}
	c.used = true
	c.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>grit</title><body style="font-family:system-ui;padding:40px">`+
		`<h1>grit is logged in</h1><p>You can close this tab and go back to your terminal.</p>`)
	c.done <- loginResult{Username: user, Key: key} // buffered; used guards it to once
}

func newState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func openBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}

// browserLogin runs the loopback flow and returns the key the browser sent.
// open is called with the URL to visit.
func browserLogin(server string, open func(string) error, timeout time.Duration) (loginResult, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return loginResult{}, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	state := newState()
	cb := newCallback(state, port)
	srv := &http.Server{Handler: cb, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Shutdown(context.Background())

	u := strings.TrimRight(server, "/") + "/cli-auth?" + url.Values{"port": {strconv.Itoa(port)}, "state": {state}}.Encode()
	fmt.Println("Opening your browser to log in. If it does not open, visit:")
	fmt.Println("  " + u)
	if err := open(u); err != nil {
		fmt.Println("(could not open a browser automatically)")
	}
	select {
	case res := <-cb.done:
		return res, nil
	case <-time.After(timeout):
		return loginResult{}, errors.New("timed out waiting for the browser")
	}
}

func login(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "grit server URL")
	user := fs.String("user", "", "create this username directly, without a browser")
	key := fs.String("key", "", "use an existing API key, without a browser")
	noBrowser := fs.Bool("no-browser", false, "print the login link instead of opening a browser (for SSH)")
	_ = fs.Parse(args)
	c := config{Server: *server, Username: *user, Key: *key}
	switch {
	case c.Key != "":
	case *user != "":
		var out struct{ Key string }
		if err := call(c, "POST", "/api/v1/signup", map[string]string{"username": *user}, &out); err != nil {
			die("%v", err)
		}
		c.Key = out.Key
	default:
		open := openBrowser
		if *noBrowser {
			open = func(string) error { return nil }
		}
		res, err := browserLogin(c.Server, open, 5*time.Minute)
		if err != nil {
			die("%v", err)
		}
		c.Username, c.Key = res.Username, res.Key
	}
	if err := saveJSON("config.json", c); err != nil {
		die("%v", err)
	}
	fmt.Printf("logged in as %s on %s. Your profile: %s/u/%s\n", c.Username, c.Server, strings.TrimRight(c.Server, "/"), c.Username)
}
