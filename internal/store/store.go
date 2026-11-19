// Package store is the server's SQLite database.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/n1k0s1/grit/internal/attest"
	"github.com/n1k0s1/grit/internal/model"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  key_hash TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  name TEXT NOT NULL,
  remote TEXT NOT NULL DEFAULT '',
  UNIQUE(user_id, name)
);
CREATE TABLE IF NOT EXISTS sessions (
  user_id INTEGER NOT NULL REFERENCES users(id),
  id TEXT NOT NULL,
  project_id INTEGER NOT NULL REFERENCES projects(id),
  harness TEXT NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  branch TEXT NOT NULL DEFAULT '',
  start_at INTEGER NOT NULL,
  end_at INTEGER NOT NULL,
  wall_s INTEGER NOT NULL,
  agent_s INTEGER NOT NULL,
  credited_s INTEGER NOT NULL,
  prompts INTEGER NOT NULL,
  flags TEXT NOT NULL DEFAULT '[]',
  body TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, id)
);
CREATE INDEX IF NOT EXISTS sessions_start ON sessions(start_at);
CREATE TABLE IF NOT EXISTS commits (
  user_id INTEGER NOT NULL,
  project_id INTEGER NOT NULL,
  sha TEXT NOT NULL,
  at INTEGER NOT NULL,
  subject TEXT NOT NULL,
  additions INTEGER NOT NULL,
  deletions INTEGER NOT NULL,
  PRIMARY KEY (user_id, sha)
);
CREATE TABLE IF NOT EXISTS links (
  user_id INTEGER NOT NULL,
  session_id TEXT NOT NULL,
  idx INTEGER NOT NULL,
  hash TEXT NOT NULL,
  first_seen INTEGER NOT NULL,
  PRIMARY KEY (user_id, session_id, idx, hash)
);
CREATE TABLE IF NOT EXISTS submissions (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  project_id INTEGER NOT NULL REFERENCES projects(id),
  created_at INTEGER NOT NULL,
  credited_s INTEGER NOT NULL,
  wall_s INTEGER NOT NULL,
  approved_s INTEGER,
  status TEXT NOT NULL DEFAULT 'pending',
  note TEXT NOT NULL DEFAULT '',
  bundle TEXT NOT NULL
);
`

// columns added after the first release, created on open if missing.
var migrations = []struct{ table, column, def string }{
	{"sessions", "witness", "TEXT NOT NULL DEFAULT ''"},
	{"sessions", "signal", "TEXT NOT NULL DEFAULT ''"},
	{"sessions", "tier", "TEXT NOT NULL DEFAULT 'low'"},
}

func migrate(db *sql.DB) error {
	for _, m := range migrations {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, m.table, m.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + m.table + ` ADD COLUMN ` + m.column + ` ` + m.def); err != nil {
				return err
			}
		}
	}
	return nil
}

// Store wraps the database handle.
type Store struct {
	db  *sql.DB
	Now func() time.Time // the server clock; tests replace it
}

// User is an account.
type User struct {
	ID        int64     `json:"-"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrNotFound is returned for missing rows.
var ErrNotFound = errors.New("not found")

// Open opens or creates the database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite has one writer; this keeps it simple and safe
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db, Now: time.Now}, nil
}

func hashKey(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:])
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9_\-]{2,32}$`)

// CreateUser makes an account and returns its API key (shown once).
func (s *Store) CreateUser(username string) (string, error) {
	if !validName.MatchString(username) {
		return "", errors.New("username must be 2-32 letters, digits, _ or -")
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	key := "grit_" + hex.EncodeToString(b)
	_, err := s.db.Exec(`INSERT INTO users(username, key_hash, created_at) VALUES(?,?,?)`,
		username, hashKey(key), time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return "", errors.New("username taken")
	}
	return key, err
}

// UserByKey authenticates an API key.
func (s *Store) UserByKey(key string) (User, error) {
	return s.user(`key_hash = ?`, hashKey(key))
}

// UserByName looks up a public profile.
func (s *Store) UserByName(name string) (User, error) {
	return s.user(`username = ?`, name)
}

func (s *Store) user(where string, arg any) (User, error) {
	var u User
	var created int64
	err := s.db.QueryRow(`SELECT id, username, created_at FROM users WHERE `+where, arg).Scan(&u.ID, &u.Username, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	u.CreatedAt = time.Unix(created, 0).UTC()
	return u, err
}

func (s *Store) project(tx *sql.Tx, userID int64, name, remote string) (int64, error) {
	if name == "" {
		name = "unknown"
	}
	_, err := tx.Exec(`INSERT INTO projects(user_id, name, remote) VALUES(?,?,?)
		ON CONFLICT(user_id, name) DO UPDATE SET remote = CASE WHEN excluded.remote != '' THEN excluded.remote ELSE projects.remote END`,
		userID, name, remote)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(`SELECT id FROM projects WHERE user_id = ? AND name = ?`, userID, name).Scan(&id)
	return id, err
}

// UpsertSessions stores synced sessions. Callers must pass Public() results.
func (s *Store) UpsertSessions(userID int64, rs []model.Result) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	nowT := s.Now()
	now := nowT.Unix()
	for _, r := range rs {
		pid, err := s.project(tx, userID, r.Project, r.Remote)
		if err != nil {
			return err
		}
		witness, err := recordLinks(tx, userID, r, nowT)
		if err != nil {
			return err
		}
		tier := model.Tier(witness == string(attest.Witnessed), r.Signal == "activity")
		flags, _ := json.Marshal(nonNil(r.Flags))
		body, _ := json.Marshal(r)
		_, err = tx.Exec(`INSERT INTO sessions(user_id,id,project_id,harness,model,branch,start_at,end_at,wall_s,agent_s,credited_s,prompts,flags,body,updated_at,witness,signal,tier)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(user_id,id) DO UPDATE SET project_id=excluded.project_id, harness=excluded.harness, model=excluded.model,
			branch=excluded.branch, start_at=excluded.start_at, end_at=excluded.end_at, wall_s=excluded.wall_s, agent_s=excluded.agent_s,
			credited_s=excluded.credited_s, prompts=excluded.prompts, flags=excluded.flags, body=excluded.body, updated_at=excluded.updated_at,
			witness=excluded.witness, signal=excluded.signal, tier=excluded.tier`,
			userID, r.SessionID, pid, r.Harness, r.Model, r.Branch, r.Start.Unix(), r.End.Unix(), r.WallSec, r.AgentSec,
			r.CreditedSec, r.Prompts, string(flags), string(body), now, witness, r.Signal, tier)
		if err != nil {
			return err
		}
		for _, c := range r.Commits {
			_, err = tx.Exec(`INSERT OR IGNORE INTO commits(user_id,project_id,sha,at,subject,additions,deletions) VALUES(?,?,?,?,?,?,?)`,
				userID, pid, c.SHA, c.At.Unix(), c.Subject, c.Additions, c.Deletions)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

var hexLink = regexp.MustCompile(`^[0-9a-f]{64}$`)

// recordLinks stores each turn's attestation link with the time the server
// first saw it (later syncs never move that time), then works out whether
// the whole session was witnessed live. Sessions without links (older
// clients, hand-written sessions) return "".
func recordLinks(tx *sql.Tx, userID int64, r model.Result, now time.Time) (string, error) {
	has := false
	status := attest.Witnessed
	for i, t := range r.Turns {
		if !hexLink.MatchString(t.Link) {
			status = attest.Backfilled
			continue
		}
		has = true
		if _, err := tx.Exec(`INSERT OR IGNORE INTO links(user_id, session_id, idx, hash, first_seen) VALUES(?,?,?,?,?)`,
			userID, r.SessionID, i, t.Link, now.UnixMilli()); err != nil {
			return "", err
		}
		var first int64
		if err := tx.QueryRow(`SELECT first_seen FROM links WHERE user_id = ? AND session_id = ? AND idx = ? AND hash = ?`,
			userID, r.SessionID, i, t.Link).Scan(&first); err != nil {
			return "", err
		}
		if attest.Timing(t.PromptAt, time.UnixMilli(first), attest.Window) != attest.Witnessed {
			status = attest.Backfilled
		}
	}
	if !has {
		return "", nil
	}
	return string(status), nil
}

// SeenLinks returns every link recorded for a session, by turn index, for
// verifying a submission.
func (s *Store) SeenLinks(userID int64, sessionID string) (map[int][]attest.Seen, error) {
	rows, err := s.db.Query(`SELECT idx, hash, first_seen FROM links WHERE user_id = ? AND session_id = ? ORDER BY first_seen`, userID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int][]attest.Seen{}
	for rows.Next() {
		var idx int
		var h string
		var first int64
		if err := rows.Scan(&idx, &h, &first); err != nil {
			return out, err
		}
		out[idx] = append(out[idx], attest.Seen{Hash: h, FirstSeen: time.UnixMilli(first).UTC()})
	}
	return out, rows.Err()
}

func nonNil(x []string) []string {
	if x == nil {
		return []string{}
	}
	return x
}

// SessionRow is a session as listed on a profile.
type SessionRow struct {
	ID       string   `json:"id"`
	Project  string   `json:"project"`
	Harness  string   `json:"harness"`
	Model    string   `json:"model"`
	Start    int64    `json:"start"`
	End      int64    `json:"end"`
	Wall     int      `json:"wall"`
	Agent    int      `json:"agent"`
	Credited int      `json:"credited"`
	Prompts  int      `json:"prompts"`
	Flags    []string `json:"flags"`
	Witness  string   `json:"witness"` // witnessed, backfilled or "" (no attestation)
	Signal   string   `json:"signal"`  // "activity" when scored with keyboard and mouse data
	Tier     string   `json:"tier"`    // high, medium or low
}

// ProjectRow is a project summary.
type ProjectRow struct {
	Name       string `json:"name"`
	Remote     string `json:"remote"`
	Credited   int    `json:"credited"`
	Wall       int    `json:"wall"`
	Sessions   int    `json:"sessions"`
	LastActive int64  `json:"last_active"`
	Commits    int    `json:"commits"`
}

// SubmissionRow is a submission summary.
type SubmissionRow struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	Project   string `json:"project"`
	Created   int64  `json:"created"`
	Credited  int    `json:"credited"`
	Wall      int    `json:"wall"`
	Approved  *int   `json:"approved"`
	Status    string `json:"status"`
	Note      string `json:"note"`
	Sessions  int    `json:"sessions"`
	FlagCount int    `json:"flags"`
}

// Profile is everything the profile page needs.
type Profile struct {
	User        User            `json:"user"`
	Sessions    []SessionRow    `json:"sessions"`
	Projects    []ProjectRow    `json:"projects"`
	Submissions []SubmissionRow `json:"submissions"`
}

// Profile loads a user's public data.
func (s *Store) Profile(u User) (Profile, error) {
	p := Profile{User: u, Sessions: []SessionRow{}, Projects: []ProjectRow{}, Submissions: []SubmissionRow{}}
	rows, err := s.db.Query(`SELECT s.id, p.name, s.harness, s.model, s.start_at, s.end_at, s.wall_s, s.agent_s, s.credited_s, s.prompts, s.flags,
		s.witness, s.signal, s.tier
		FROM sessions s JOIN projects p ON p.id = s.project_id WHERE s.user_id = ? ORDER BY s.start_at DESC LIMIT 1000`, u.ID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var r SessionRow
		var flags string
		if err := rows.Scan(&r.ID, &r.Project, &r.Harness, &r.Model, &r.Start, &r.End, &r.Wall, &r.Agent, &r.Credited, &r.Prompts, &flags,
			&r.Witness, &r.Signal, &r.Tier); err != nil {
			rows.Close()
			return p, err
		}
		_ = json.Unmarshal([]byte(flags), &r.Flags)
		p.Sessions = append(p.Sessions, r)
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT p.name, p.remote, COALESCE(SUM(s.credited_s),0), COALESCE(SUM(s.wall_s),0), COUNT(s.id), COALESCE(MAX(s.end_at),0),
		(SELECT COUNT(*) FROM commits c WHERE c.project_id = p.id)
		FROM projects p LEFT JOIN sessions s ON s.project_id = p.id WHERE p.user_id = ? GROUP BY p.id ORDER BY 3 DESC`, u.ID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var r ProjectRow
		if err := rows.Scan(&r.Name, &r.Remote, &r.Credited, &r.Wall, &r.Sessions, &r.LastActive, &r.Commits); err != nil {
			rows.Close()
			return p, err
		}
		p.Projects = append(p.Projects, r)
	}
	rows.Close()

	if n := len(p.Sessions); n > 0 {
		if first := time.Unix(p.Sessions[n-1].Start, 0).UTC(); first.Before(p.User.CreatedAt) {
			p.User.CreatedAt = first
		}
	}
	subs, err := s.Submissions(`s.user_id = ?`, u.ID)
	p.Submissions = subs
	return p, err
}

// Submissions lists submissions matching a filter.
func (s *Store) Submissions(where string, args ...any) ([]SubmissionRow, error) {
	out := []SubmissionRow{}
	rows, err := s.db.Query(`SELECT s.id, u.username, p.name, s.created_at, s.credited_s, s.wall_s, s.approved_s, s.status, s.note,
		json_array_length(s.bundle, '$.sessions'),
		(SELECT COUNT(*) FROM json_each(s.bundle, '$.sessions') je WHERE json_array_length(je.value, '$.flags') > 0)
		FROM submissions s JOIN users u ON u.id = s.user_id JOIN projects p ON p.id = s.project_id
		WHERE `+where+` ORDER BY s.created_at DESC`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SubmissionRow
		var approved sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Username, &r.Project, &r.Created, &r.Credited, &r.Wall, &approved, &r.Status, &r.Note, &r.Sessions, &r.FlagCount); err != nil {
			return out, err
		}
		if approved.Valid {
			v := int(approved.Int64)
			r.Approved = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Session returns one stored (public) session.
func (s *Store) Session(u User, id string) (model.Result, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM sessions WHERE user_id = ? AND id = ?`, u.ID, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Result{}, ErrNotFound
	}
	var r model.Result
	if err == nil {
		err = json.Unmarshal([]byte(body), &r)
	}
	return r, err
}

// SessionTrust returns a stored session's attestation status and trust tier.
func (s *Store) SessionTrust(u User, id string) (witness, tier string, err error) {
	err = s.db.QueryRow(`SELECT witness, tier FROM sessions WHERE user_id = ? AND id = ?`, u.ID, id).Scan(&witness, &tier)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// ProjectSessions returns a project's sessions and commits.
func (s *Store) ProjectSessions(u User, project string) (ProjectRow, []SessionRow, []model.Commit, error) {
	var pr ProjectRow
	var pid int64
	err := s.db.QueryRow(`SELECT id, name, remote FROM projects WHERE user_id = ? AND name = ?`, u.ID, project).Scan(&pid, &pr.Name, &pr.Remote)
	if errors.Is(err, sql.ErrNoRows) {
		return pr, nil, nil, ErrNotFound
	}
	if err != nil {
		return pr, nil, nil, err
	}
	prof, err := s.Profile(u)
	if err != nil {
		return pr, nil, nil, err
	}
	var ss []SessionRow
	for _, r := range prof.Sessions {
		if r.Project == project {
			ss = append(ss, r)
			pr.Credited += r.Credited
			pr.Wall += r.Wall
			pr.Sessions++
		}
	}
	var cs []model.Commit
	rows, err := s.db.Query(`SELECT sha, at, subject, additions, deletions FROM commits WHERE project_id = ? ORDER BY at DESC`, pid)
	if err != nil {
		return pr, ss, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c model.Commit
		var at int64
		if err := rows.Scan(&c.SHA, &at, &c.Subject, &c.Additions, &c.Deletions); err != nil {
			return pr, ss, cs, err
		}
		c.At = time.Unix(at, 0).UTC()
		cs = append(cs, c)
	}
	pr.Commits = len(cs)
	return pr, ss, cs, rows.Err()
}

// Bundle is what `grit submit` uploads: full sessions with redacted prompts.
type Bundle struct {
	Project  string         `json:"project"`
	Remote   string         `json:"remote"`
	Note     string         `json:"note"`
	Sessions []model.Result `json:"sessions"`
}

// CreateSubmission stores a bundle for review.
func (s *Store) CreateSubmission(userID int64, b Bundle) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	pid, err := s.project(tx, userID, b.Project, b.Remote)
	if err != nil {
		return 0, err
	}
	var cred, wall int
	for _, r := range b.Sessions {
		cred += r.CreditedSec
		wall += r.WallSec
	}
	body, _ := json.Marshal(b)
	res, err := tx.Exec(`INSERT INTO submissions(user_id, project_id, created_at, credited_s, wall_s, bundle) VALUES(?,?,?,?,?,?)`,
		userID, pid, time.Now().Unix(), cred, wall, string(body))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

// Submission loads one submission with its bundle.
func (s *Store) Submission(id int64) (SubmissionRow, Bundle, error) {
	var b Bundle
	rs, err := s.Submissions(`s.id = ?`, id)
	if err != nil {
		return SubmissionRow{}, b, err
	}
	if len(rs) == 0 {
		return SubmissionRow{}, b, ErrNotFound
	}
	var body string
	if err := s.db.QueryRow(`SELECT bundle FROM submissions WHERE id = ?`, id).Scan(&body); err != nil {
		return rs[0], b, err
	}
	err = json.Unmarshal([]byte(body), &b)
	return rs[0], b, err
}

// Review records a reviewer's decision.
func (s *Store) Review(id int64, status string, approvedSec int, note string) error {
	_, err := s.db.Exec(`UPDATE submissions SET status = ?, approved_s = ?, note = ? WHERE id = ?`, status, approvedSec, note, id)
	return err
}

// LeaderRow is one line of the leaderboard.
type LeaderRow struct {
	Username string `json:"username"`
	Credited int    `json:"credited"`
	Wall     int    `json:"wall"`
	Agent    int    `json:"agent"`
	Projects int    `json:"projects"`
	Top      string `json:"top"`
}

// Leaderboard ranks users by credited time since a moment.
func (s *Store) Leaderboard(since time.Time) ([]LeaderRow, error) {
	out := []LeaderRow{}
	rows, err := s.db.Query(`SELECT u.username, SUM(s.credited_s), SUM(s.wall_s), SUM(s.agent_s), COUNT(DISTINCT s.project_id),
		(SELECT p.name FROM sessions s2 JOIN projects p ON p.id = s2.project_id WHERE s2.user_id = u.id AND s2.start_at >= ?
		 GROUP BY p.id ORDER BY SUM(s2.credited_s) DESC LIMIT 1)
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.start_at >= ? GROUP BY u.id ORDER BY 2 DESC LIMIT 50`,
		since.Unix(), since.Unix())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r LeaderRow
		if err := rows.Scan(&r.Username, &r.Credited, &r.Wall, &r.Agent, &r.Projects, &r.Top); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Totals are site-wide numbers for the home page.
type Totals struct {
	Users    int `json:"users"`
	Credited int `json:"credited"`
	Wall     int `json:"wall"`
	Sessions int `json:"sessions"`
}

// SiteTotals sums everything.
func (s *Store) SiteTotals() (Totals, error) {
	var t Totals
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM users), COALESCE(SUM(credited_s),0), COALESCE(SUM(wall_s),0), COUNT(*) FROM sessions`).
		Scan(&t.Users, &t.Credited, &t.Wall, &t.Sessions)
	return t, err
}
