// Package acatest is an AI coding agent for tests, which run neither a real
// AI CLI nor the network: its program is a shell.
package acatest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/aca"
	"github.com/srjn45/wdx/internal/atomicfile"
)

// defaultScript runs the file run in the agent's dir, if there is one, else
// stays until it is hung up on.
const defaultScript = "if [ -f run ]; then . ./run; fi; exec sleep 1000"

// ACA runs Script in /bin/sh, after appending the session it was given to
// the file Launches, one line per start.
type ACA struct {
	aca.NoSteer
	// Launches, when set, records each start's session.
	Launches string
	// Notes, when set, records each start's note, one line each.
	Notes string
	// Kept, when set, records each start's model and permissions, one line each.
	Kept string
	// Script is what its shell runs; defaultScript when "".
	Script string
	// Conversation, when set, is the file BackUp takes the transcript from, and
	// Session the one holding the session it says it is in now.
	Conversation string
	Session      string
	// Dir, when set, is the file holding where it says it last worked.
	Dir string
	// Running is what Detached finds, and Stopped what StopDetached took from it.
	Running []aca.DetachedSession
	Stopped []string
	// ProgramName is what Program says, "sh" when "": a name never installed makes
	// it an ACA that is not.
	ProgramName string
	// ContextFiles, when set, is a directory outside the backup where ContextSize
	// reads the size of session l.SessionID from the file named after it, which it
	// names to follow, as Codex's is read from its session file: a size a line, the
	// last one counting, none yet in an empty file. "" reads ContextFile in the
	// backup, as Claude Code's is.
	ContextFiles string
	// BriefOf, when set, is the ACA whose Brief it gives, Claude Code's when nil.
	BriefOf aca.AiCodingAgent
}

// ID implements aca.AiCodingAgent.
func (a *ACA) ID() string { return aca.ClaudeCode{}.ID() }

// Program implements aca.AiCodingAgent.
func (a *ACA) Program() string { return cmp.Or(a.ProgramName, "sh") }

// ScreenParser implements aca.AiCodingAgent: Claude Code's, as a holder goes by
// the id.
func (a *ACA) ScreenParser() aca.ScreenParser { return aca.ClaudeCodeScreenParser{} }

// Deliver implements aca.AiCodingAgent: Claude Code's, as a holder goes by the
// id.
func (a *ACA) Deliver(w io.Writer, message string) error { return aca.ClaudeCode{}.Deliver(w, message) }

// Interrupt implements aca.AiCodingAgent: Claude Code's, as a holder goes by the
// id.
func (a *ACA) Interrupt() []byte { return aca.ClaudeCode{}.Interrupt() }

// BackUp implements aca.AiCodingAgent.
func (a *ACA) BackUp(_ context.Context, l aca.Launch) (aca.Backup, error) {
	backup := aca.Backup{Session: l.SessionID}
	if data, err := os.ReadFile(a.Session); err == nil {
		backup.Session = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(a.Dir); err == nil {
		backup.Dir = strings.TrimSpace(string(data))
	}
	data, err := os.ReadFile(a.Conversation)
	if errors.Is(err, os.ErrNotExist) || a.Conversation == "" {
		return backup, nil
	}
	if err != nil {
		return aca.Backup{}, err
	}
	return backup, os.WriteFile(filepath.Join(l.BackupDir, aca.TranscriptFile), data, 0o600)
}

// BeginSession implements aca.AiCodingAgent: what a says it is in, if it says. The
// old session's ContextFile goes, as Claude Code's status file does; a session's file
// in ContextFiles is no longer the agent's, as Codex's is not.
func (a *ACA) BeginSession(_ context.Context, l aca.Launch, session string) error {
	if err := os.Remove(filepath.Join(l.BackupDir, ContextFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if a.Session == "" {
		return nil
	}
	return os.WriteFile(a.Session, []byte(session+"\n"), 0o600)
}

// Brief implements aca.AiCodingAgent: BriefOf's, or Claude Code's, so that a test can
// give it what that ACA's backup writes.
func (a *ACA) Brief(ctx context.Context, l aca.Launch, limit int) (aca.Brief, error) {
	if a.BriefOf != nil {
		return a.BriefOf.Brief(ctx, l, limit)
	}
	return aca.ClaudeCode{}.Brief(ctx, l, limit)
}

// Detached implements aca.AiCodingAgent.
func (a *ACA) Detached(context.Context, aca.Launch) ([]aca.DetachedSession, error) {
	return a.Running, nil
}

// StopDetached implements aca.AiCodingAgent.
func (a *ACA) StopDetached(_ context.Context, _ aca.Launch, d aca.DetachedSession) error {
	a.Running = slices.DeleteFunc(a.Running, func(r aca.DetachedSession) bool { return r.ID == d.ID })
	a.Stopped = append(a.Stopped, d.Session)
	return nil
}

// ContextFile is the file in an agent's backup ACA's ContextSize reads: the tokens
// and the window, as two numbers.
const ContextFile = "context-size"

// ContextSize implements aca.AiCodingAgent: what ContextFile in the backup says, or
// with ContextFiles the session's file there, named to follow once it exists.
func (a *ACA) ContextSize(l aca.Launch) (aca.ContextSize, []string, error) {
	if a.ContextFiles == "" {
		size, err := readContextSize(filepath.Join(l.BackupDir, ContextFile))
		return size, nil, err
	}
	file := filepath.Join(a.ContextFiles, l.SessionID)
	size, err := readContextSize(file)
	if errors.Is(err, os.ErrNotExist) {
		return aca.ContextSize{}, nil, aca.ErrNoContextSize
	}
	return size, []string{file}, err
}

// readContextSize is the size the last line of path says; aca.ErrNoContextSize when
// it is empty, and os.ErrNotExist when there is none.
func readContextSize(path string) (aca.ContextSize, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return aca.ContextSize{}, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return aca.ContextSize{}, aca.ErrNoContextSize
	}
	var size aca.ContextSize
	if _, err := fmt.Sscan(text[strings.LastIndexByte(text, '\n')+1:], &size.Tokens, &size.Window); err != nil {
		return aca.ContextSize{}, fmt.Errorf("read %s: %w", path, err)
	}
	return size, nil
}

// WriteContextSize writes size as ContextSize reads it into backup, whole, as Claude
// Code's status line does.
func WriteContextSize(backup string, size aca.ContextSize) error {
	return atomicfile.Write(filepath.Join(backup, ContextFile), contextLine(size), 0o600)
}

// AppendContextSize adds size to the end of the file at path, in ContextFiles, as Codex
// appends to its session file.
func AppendContextSize(path string, size aca.ContextSize) error {
	return appendLine(path, strings.TrimSuffix(string(contextLine(size)), "\n"))
}

func contextLine(size aca.ContextSize) []byte {
	return fmt.Appendf(nil, "%d %d\n", size.Tokens, size.Window)
}

// Models implements aca.AiCodingAgent: Claude Code's, as its id is.
func (a *ACA) Models(ctx context.Context, l aca.Launch) ([]aca.Model, error) {
	return aca.ClaudeCode{}.Models(ctx, l)
}

// Command implements aca.AiCodingAgent. It makes the backup, as Claude Code's
// writes its settings there.
func (a *ACA) Command(_ context.Context, l aca.Launch) ([]string, error) {
	if l.BackupDir != "" {
		if err := os.MkdirAll(l.BackupDir, 0o700); err != nil {
			return nil, err
		}
	}
	if a.Launches != "" {
		f, err := os.OpenFile(a.Launches, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := fmt.Fprintln(f, l.SessionID); err != nil {
			return nil, err
		}
	}
	if a.Notes != "" {
		if err := appendLine(a.Notes, l.Note); err != nil {
			return nil, err
		}
	}
	if a.Kept != "" {
		if err := appendLine(a.Kept, l.Model+" "+string(l.Permissions)); err != nil {
			return nil, err
		}
	}
	script := a.Script
	if script == "" {
		script = defaultScript
	}
	return []string{"/bin/sh", "-c", script}, nil
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, line)
	return errors.Join(err, f.Close())
}

// NotesGiven returns the notes a's Notes recorded, oldest first.
func (a *ACA) NotesGiven() ([]string, error) {
	data, err := os.ReadFile(a.Notes)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

// KeptGiven returns the model and permissions of each start, as "<model> <permissions>",
// oldest first.
func (a *ACA) KeptGiven() ([]string, error) {
	data, err := os.ReadFile(a.Kept)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

// Sessions returns the sessions a's Launches recorded, oldest first.
func (a *ACA) Sessions() ([]string, error) {
	data, err := os.ReadFile(a.Launches)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}

// Bare is an AI coding agent that has written nothing of its own but the
// command: every stub of package aca and its NoScreenParser, as a new ACA starts.
// Its program is a shell, as ACA's.
type Bare struct {
	aca.NoBackUp
	aca.NoBeginSession
	aca.NoBrief
	aca.NoContextSize
	aca.NoDetached
	aca.NoSteer
	// Name is its id, and ProgramName what Program says, "sh" when "".
	Name, ProgramName string
}

// ID implements aca.AiCodingAgent.
func (b *Bare) ID() string { return b.Name }

// Program implements aca.AiCodingAgent.
func (b *Bare) Program() string { return cmp.Or(b.ProgramName, "sh") }

// Command implements aca.AiCodingAgent.
func (b *Bare) Command(context.Context, aca.Launch) ([]string, error) {
	return []string{"/bin/sh", "-c", defaultScript}, nil
}

// ScreenParser implements aca.AiCodingAgent.
func (b *Bare) ScreenParser() aca.ScreenParser { return aca.NoScreenParser{} }

// Deliver implements aca.AiCodingAgent: the message typed, then Enter.
func (b *Bare) Deliver(w io.Writer, message string) error {
	_, err := io.WriteString(w, message+"\r")
	return err
}

// Interrupt implements aca.AiCodingAgent.
func (b *Bare) Interrupt() []byte { return []byte("\x03") }

// Models implements aca.AiCodingAgent.
func (b *Bare) Models(context.Context, aca.Launch) ([]aca.Model, error) {
	return []aca.Model{{ID: "bare-1", Name: "Bare One", Default: true}}, nil
}

// NotInstalled is a program LookProgram never finds: an ACA whose ProgramName it is
// is not installed.
const NotInstalled = "wdx-test-not-installed"

// LookProgram stands in for the daemon's look through the login shell, which a
// test's shell, a script, does not take: it finds every program but NotInstalled.
func LookProgram(_ context.Context, program string) error {
	if program == NotInstalled {
		return fmt.Errorf("%s: not found", program)
	}
	return nil
}
