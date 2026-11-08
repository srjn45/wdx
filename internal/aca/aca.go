// Package aca holds what differs between the AI coding agents wdx runs: Claude
// Code, Antigravity, Cursor, Codex and others. See docs/AGENT.md and docs/ACA.md.
package aca

import (
	"context"
	"io"
	"slices"
	"time"
)

// AiCodingAgent is one AI CLI wdx can run in an agent.
type AiCodingAgent interface {
	// ID names it in the database and the API, as "claude_code".
	ID() string
	// Program is the program Command runs, as "claude": what wdx looks for to
	// know it is installed.
	Program() string
	// Command is what to run: the program, or env and the program; it starts
	// the session or resumes it. It runs through the user's login shell, which
	// finds the program.
	Command(context.Context, Launch) ([]string, error)
	// ScreenParser reads what it is doing from its screen.
	ScreenParser() ScreenParser
	// Deliver puts one message into its input box on w, its terminal's input,
	// and submits it. Everything a message needs waits here, and the holder runs
	// it apart from what must not wait.
	Deliver(w io.Writer, message string) error
	// Interrupt is the input that stops its turn.
	Interrupt() []byte
	// Steer gives one message to the turn running, on w, without stopping it, or
	// submits it when none runs: how an instant message reaches an ACA that can,
	// in place of Interrupt then Deliver. One that cannot embeds NoSteer.
	// docs/SEND.md.
	Steer(w io.Writer, message string) error
	// BackUp brings the backup in l.BackupDir up to date with what it keeps of
	// the conversation, TranscriptFile among it, and says what it learned. What it
	// keeps and how it says so differ per AI coding agent. docs/AGENT_RESUME.md.
	BackUp(ctx context.Context, l Launch) (Backup, error)
	// BeginSession makes session, which has not started, the one the agent goes on
	// with from l.SessionID, and remembers that a restart began it, not the human. Its
	// process is gone. docs/AGENT_RESUME.md.
	BeginSession(ctx context.Context, l Launch, session string) error
	// Brief is the conversation in about limit bytes, for an agent starting a new
	// session to read: from the latest summary, or the first request, the messages
	// since, and each tool call as one line. An AI coding agent with no summary starts
	// from the first request. docs/AGENT_RESUME.md.
	Brief(ctx context.Context, l Launch, limit int) (Brief, error)
	// Detached is what of the agent's conversation still runs outside its terminal,
	// where stopping the terminal does not reach it. Most agents have none, and finding
	// that out starts nothing. docs/AGENT_RESUME.md.
	Detached(ctx context.Context, l Launch) ([]DetachedSession, error)
	// StopDetached stops one of what Detached found. Its conversation stays.
	StopDetached(ctx context.Context, l Launch, d DetachedSession) error
	// Models is what it offers this user now, as it says, in its order; l gives only
	// Shell and UserHome, for one that runs its CLI to ask.
	Models(ctx context.Context, l Launch) ([]Model, error)
	// ContextSize returns how full the agent's context is, as its ACA last told,
	// and the files outside l.BackupDir it read, which the daemon follows too;
	// ErrNoContextSize when it has told nothing yet, with the files still named when
	// known. The daemon reads it each time the backup or a followed file changes, and
	// when l.SessionID changes, at most once a second. docs/AGENT.md.
	ContextSize(l Launch) (size ContextSize, follow []string, err error)
	// Usage returns what the ACA last told of its account's limits, a row per window
	// and bucket, and the files outside l.BackupDir it read, which the daemon follows
	// too; ErrNoUsage when it has told nothing, as on a plan without limits. The
	// daemon reads it with ContextSize, from the same changes. The wdx doc USAGE.
	Usage(l Launch) (rows []Usage, follow []string, err error)
}

// Usage is how much of one limit of an ACA's account is used: a window, of a bucket
// of models when the ACA meters some apart. It belongs to the account, not to the
// agent it was read from.
type Usage struct {
	Window      string    // the window's key as the ACA gives it, as five_hour
	Bucket      string    // the bucket's name as the ACA gives it; "" for the one every model draws on
	UsedPercent int       // 0 to 100, or above past the limit, as given
	ResetsAt    time.Time // when the window resets; zero when the ACA gives none
	ObservedAt  time.Time // when the ACA told it, as best known: the file's time
	// Limited is whether the ACA said this window's bucket cannot be used now,
	// whatever UsedPercent says: Codex may stop at 99 and then refuse with an error.
	Limited bool
}

// ContextSize is how full an agent's context is.
type ContextSize struct {
	Tokens int64 // in the context now
	Window int64 // the model's window; 0 when the ACA does not tell
}

// Model is one model an AI coding agent offers.
type Model struct {
	ID      string // what its model flag takes, and Launch.Model carries
	Name    string // what a person reads
	Default bool   // whether it runs this one when given none
}

// DetachedSession is a session of an agent running outside its terminal.
type DetachedSession struct {
	ID      string // what the AI coding agent calls the process
	Session string
	Working bool
}

// Brief is what AiCodingAgent.Brief found in TranscriptFile.
type Brief struct {
	From int64 // the line it starts at
	Next int64 // the number the next line written will take
	// Start is the message it starts at, and Rest what follows.
	Start, Rest []string
	// OmittedFrom and OmittedCount are the lines between them that did not fit, if any.
	OmittedFrom, OmittedCount int64
}

// Backup is what bringing a backup up to date learned.
type Backup struct {
	// Session is the session it is in now, which a command that resumes goes on
	// with.
	Session string
	// Dir is where it last worked, which may not be where it started; "" when it
	// does not say.
	Dir string
}

// TranscriptFile is the name of the conversation as text in a backup, oldest
// first, which only ever grows.
const TranscriptFile = "transcript.md"

// known is every AI coding agent wdx can run, in the order they are offered, and
// the one place one is made: adding one is adding it here. docs/ACA.md.
var known = []AiCodingAgent{ClaudeCode{}, Antigravity{}, Cursor{}, Codex{}}

// All is every AI coding agent wdx can run, in the order they are offered.
func All() []AiCodingAgent {
	return slices.Clone(known)
}

// IDs is the ids of All, in its order.
func IDs() []string {
	ids := make([]string, 0, len(known))
	for _, a := range known {
		ids = append(ids, a.ID())
	}
	return ids
}

// ByID is the AI coding agent named id among All.
func ByID(id string) (AiCodingAgent, bool) {
	for _, a := range known {
		if a.ID() == id {
			return a, true
		}
	}
	return nil, false
}

// Default is the first of All, for what needs one with no daemon to ask which
// are usable.
func Default() AiCodingAgent {
	return known[0]
}

// Permissions is what an agent may do without asking, kept for every start.
type Permissions string

const (
	// PermissionsFull asks for no approval, with network access and no sandbox.
	PermissionsFull Permissions = "full"
	// PermissionsAsk is the AI coding agent's own default, as the user's settings
	// for it say.
	PermissionsAsk Permissions = "ask"
)

// Launch is one start of an agent.
type Launch struct {
	Dir       string
	SessionID string
	// UserHome is the user's home directory, which holds what an AI CLI
	// keeps.
	UserHome string
	// BackupDir is the agent's backup; "" keeps none.
	BackupDir string
	// Shell is the user's login shell, which finds the AI CLI when something other than
	// Command has to run it. Only Detached and StopDetached need it.
	Shell string
	// Note is added to the AI CLI's system prompt; "" adds nothing.
	Note string
	// Model is what to pass as its model, as the user asked for it; "" is its
	// default.
	Model string
	// Permissions is what it may do without asking; "" is PermissionsAsk.
	Permissions Permissions
}
