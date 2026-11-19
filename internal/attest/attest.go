// Package attest proves when a session's turns were first seen by the
// server, without sending prompt text.
//
// Each sync carries a hash chain over the session's turns. Link i is
//
//	sha256(link[i-1] || prompt time || kind || sha256(prompt text))
//
// and link -1 is a seed made from the session ID and a per-session salt.
// The prompt text is the redacted text, exactly as `grit submit` would send
// it. The salt is derived from a secret that never leaves the machine except
// inside a submission, so the server cannot brute-force short prompts such
// as "yes" from the hashes it holds.
//
// The server records when it first saw each link. On submission it
// recomputes the chain from the bundle: a link that matches and was first
// seen within Window of its prompt is witnessed; a match seen later is
// backfilled; a link that does not match what was synced means the bundle
// was edited, and the session fails.
package attest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/n1k0s1/grit/internal/model"
)

const version = "grit-attest-v1"

// Window is how soon after a prompt its link must reach the server for the
// turn to count as witnessed.
const Window = 15 * time.Minute

// Skew is how far a prompt may appear to be in the future (client clock
// ahead of the server) and still count.
const Skew = 5 * time.Minute

// Status of a link or a whole session.
type Status string

const (
	Witnessed  Status = "witnessed"  // seen by the server within Window of happening
	Backfilled Status = "backfilled" // matches, but synced late or never
	Mismatch   Status = "mismatch"   // does not match what the server saw
)

// Salt derives a session's salt from the device secret.
func Salt(secret []byte, sessionID string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(version + "\x00salt\x00" + sessionID))
	return m.Sum(nil)
}

func seed(sessionID string, salt []byte) []byte {
	h := sha256.New()
	h.Write([]byte(version + "\x00"))
	h.Write([]byte(sessionID))
	h.Write([]byte{0})
	h.Write(salt)
	return h.Sum(nil)
}

func link(prev []byte, at time.Time, kind, text string) []byte {
	th := sha256.Sum256([]byte(text))
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(at.UTC().UnixNano()))
	h := sha256.New()
	h.Write(prev)
	h.Write(ts[:])
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write(th[:])
	return h.Sum(nil)
}

// Chain returns the hex links for a session's turns, in order. Each turn's
// PromptText must already be redacted.
func Chain(sessionID string, salt []byte, turns []model.Turn) []string {
	out := make([]string, len(turns))
	prev := seed(sessionID, salt)
	for i, t := range turns {
		prev = link(prev, t.PromptAt, string(model.Prompt), t.PromptText)
		out[i] = hex.EncodeToString(prev)
	}
	return out
}

// Seen is one link the server recorded for a turn index.
type Seen struct {
	Hash      string
	FirstSeen time.Time
}

// LinkResult is the verdict for one turn.
type LinkResult struct {
	Index     int       `json:"i"`
	Status    Status    `json:"status"`
	FirstSeen time.Time `json:"first_seen,omitempty"`
}

// Timing says whether a link first seen at firstSeen witnessed a prompt at promptAt.
func Timing(promptAt, firstSeen time.Time, window time.Duration) Status {
	d := firstSeen.Sub(promptAt)
	if d <= window && d >= -Skew {
		return Witnessed
	}
	return Backfilled
}

// Verify recomputes a bundle session's chain and compares it with what the
// server recorded. seen(i) returns every link recorded at index i.
//
// A turn verifies only if its link is the first one the server ever saw at
// that index. A later, different link at the same index means the session
// was rewritten after it was first synced, so it fails even if the
// rewritten bundle carries matching links of its own.
func Verify(sessionID string, salt []byte, turns []model.Turn, seen func(int) []Seen, window time.Duration) (Status, []LinkResult) {
	chain := Chain(sessionID, salt, turns)
	out := make([]LinkResult, len(turns))
	status := Witnessed
	for i, h := range chain {
		res := LinkResult{Index: i, Status: Backfilled}
		if recorded := seen(i); len(recorded) > 0 {
			first := recorded[0]
			for _, s := range recorded[1:] {
				if s.FirstSeen.Before(first.FirstSeen) {
					first = s
				}
			}
			if first.Hash == h {
				res.FirstSeen = first.FirstSeen
				res.Status = Timing(turns[i].PromptAt, first.FirstSeen, window)
			} else {
				res.Status = Mismatch
			}
		}
		out[i] = res
		switch {
		case res.Status == Mismatch:
			status = Mismatch
		case res.Status == Backfilled && status == Witnessed:
			status = Backfilled
		}
	}
	return status, out
}

// LoadSecret reads the device secret from dir/secret, creating it (32
// random bytes, readable only by the user) on first use.
func LoadSecret(dir string) ([]byte, error) {
	p := filepath.Join(dir, "secret")
	b, err := os.ReadFile(p)
	if err == nil && len(b) == 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return b, os.WriteFile(p, b, 0o600)
}
