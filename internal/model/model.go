// Package model holds the types shared by the client, the scorer and the server.
package model

import "time"

// Kind is what produced an event in an agent session.
type Kind string

const (
	// Prompt is a message the human wrote.
	Prompt Kind = "prompt"
	// Agent is agent output: text, thinking, or a tool result coming back.
	Agent Kind = "agent"
	// Tool is a tool call made by the agent.
	Tool Kind = "tool"
)

// Event is one normalised moment in a session. Collectors turn each harness's
// own log format into a list of these.
type Event struct {
	At      time.Time `json:"at"`
	Kind    Kind      `json:"kind"`
	Text    string    `json:"text,omitempty"`    // prompt text, never leaves the machine unless submitted
	Tool    string    `json:"tool,omitempty"`    // tool name for Tool events
	File    string    `json:"file,omitempty"`    // file the tool touched, if any
	Command string    `json:"command,omitempty"` // shell command, if any
}

// Session is one conversation with one agent harness.
type Session struct {
	ID        string
	Harness   string // "claude-code", "codex", "hook:cursor", ...
	Model     string
	Cwd       string
	Project   string
	GitBranch string
	GitRemote string
	Source    string // file the session was read from
	Events    []Event
}

// Interval is a stretch of time credited to the human.
type Interval struct {
	Start time.Time `json:"s"`
	End   time.Time `json:"e"`
	Kind  string    `json:"k"` // "think", "agent", "review", "hand"
	Rule  string    `json:"r"` // why this stretch was credited
}

// Seconds is the length of the interval in whole seconds.
func (iv Interval) Seconds() int { return int(iv.End.Sub(iv.Start).Seconds()) }

// Turn is one prompt plus the agent run that followed it.
type Turn struct {
	Index          int       `json:"i"`
	PromptAt       time.Time `json:"at"`
	AgentEnd       time.Time `json:"end"`
	PromptChars    int       `json:"chars"`
	PromptText     string    `json:"text,omitempty"`
	ThinkSec       int       `json:"think"`
	ThinkRule      string    `json:"think_rule"`
	AgentSec       int       `json:"agent"`
	AgentCreditSec int       `json:"agent_credit"`
	AgentRule      string    `json:"agent_rule"`
	ToolCalls      int       `json:"tools"`
	Files          []string  `json:"files,omitempty"`
	Commands       []string  `json:"commands,omitempty"`
	Flags          []string  `json:"flags,omitempty"`
}

// Commit is a git commit made in the project during a session.
type Commit struct {
	SHA       string    `json:"sha"`
	At        time.Time `json:"at"`
	Subject   string    `json:"subject"`
	Additions int       `json:"add"`
	Deletions int       `json:"del"`
}

// Result is a scored session. Public() strips it down to what is synced.
type Result struct {
	SessionID        string     `json:"id"`
	Harness          string     `json:"harness"`
	Model            string     `json:"model,omitempty"`
	Project          string     `json:"project"`
	Branch           string     `json:"branch,omitempty"`
	Remote           string     `json:"remote,omitempty"`
	Start            time.Time  `json:"start"`
	End              time.Time  `json:"end"`
	WallSec          int        `json:"wall"`
	AgentSec         int        `json:"agent"`
	CreditedSec      int        `json:"credited"`
	OverlapSec       int        `json:"overlap,omitempty"`
	Prompts          int        `json:"prompts"`
	ToolCalls        int        `json:"tools"`
	FilesTouched     int        `json:"files"`
	DebugPrompts     int        `json:"debug"`
	LowEffortPrompts int        `json:"low_effort"`
	SpecificPrompts  int        `json:"specific"`
	Flags            []string   `json:"flags,omitempty"`
	Signal           string     `json:"signal,omitempty"`      // "activity" when keyboard and mouse data shaped the score
	HandEdits        int        `json:"hand_edits,omitempty"`  // files written by hand (manual sessions)
	AgentEdits       int        `json:"agent_edits,omitempty"` // files written by the agent during this session
	Intervals        []Interval `json:"intervals"`
	Turns            []Turn     `json:"turns"`
	Commits          []Commit   `json:"commits,omitempty"`
}

// Public returns a copy with prompt text, file names and commands removed.
// This is what `grit sync` sends for logged (not submitted) hours.
func (r Result) Public() Result {
	out := r
	out.Turns = make([]Turn, len(r.Turns))
	for i, t := range r.Turns {
		t.PromptText = ""
		t.Files = nil
		t.Commands = nil
		out.Turns[i] = t
	}
	return out
}
