// Package doc keeps a project's docs: text kept outside its repository, every
// change a new revision, so that wdx sees each one. See docs/DOCS.md.
package doc

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"
)

// Status says whether a doc is agreed on.
type Status string

const (
	StatusWIP      Status = "wip"
	StatusReview   Status = "review"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Statuses are every status, in the order a doc goes through them.
var Statuses = []Status{StatusWIP, StatusReview, StatusApproved, StatusRejected}

// Doc is a doc's row: what it is, without its content.
type Doc struct {
	ID          string
	ProjectID   string
	ProjectName string
	Name        string
	Format      string // the extension its copies get, such as md; empty for none
	Labels      []string
	Status      Status
	Revision    int // the latest
	Size        int // of the latest revision's text
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Revision is one revision of a doc, without its content.
type Revision struct {
	Number    int
	Size      int
	CreatedAt time.Time
}

// Limits of what a doc holds.
const (
	MaxContent = 2 << 20 // bytes; the most tide's file tab shows
	MaxLabels  = 20
)

// ErrNotFound is returned when no doc, or no revision of one, matches.
var ErrNotFound = errors.New("doc not found")

// NameTakenError is returned when a doc would get a name another doc in its
// project has.
type NameTakenError struct {
	Name string
	Doc  Doc
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("name %q is already used by doc %s", e.Name, e.Doc.Name)
}

// InvalidNameError is returned for a name a doc cannot have.
type InvalidNameError struct {
	Name   string
	Reason string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("invalid name %q: %s", e.Name, e.Reason)
}

// InvalidError is returned for a format, label, status or content a doc cannot
// have; What says which.
type InvalidError struct {
	What   string
	Reason string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.What, e.Reason)
}

// ChangedError is returned when a change names a base revision that is no longer
// the doc's latest: someone changed it since it was read. Doc is the doc as it is
// now.
type ChangedError struct {
	Doc  Doc
	Base int
}

func (e *ChangedError) Error() string {
	return fmt.Sprintf("doc %s is at revision %d, not %d", e.Doc.Name, e.Doc.Revision, e.Base)
}

// Names, formats and labels are drawn in tide and printed by the cli, so they must
// not be able to move a terminal's cursor; formats and names also name files.
var (
	nameChars   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	formatChars = regexp.MustCompile(`^[A-Za-z0-9]{0,10}$`)
	labelChars  = regexp.MustCompile(`^[a-z0-9_-]{1,50}$`)
)

func checkName(name string) error {
	if name == "" {
		return &InvalidNameError{Name: name, Reason: "it is blank"}
	}
	if !nameChars.MatchString(name) {
		return &InvalidNameError{Name: name, Reason: "it can only have letters, numbers, _ and -, at most 100"}
	}
	return nil
}

func checkFormat(format string) error {
	if !formatChars.MatchString(format) {
		return &InvalidError{What: "format", Reason: fmt.Sprintf("%q can only have letters and numbers, at most 10", format)}
	}
	return nil
}

func checkStatus(s Status) error {
	if !slices.Contains(Statuses, s) {
		return &InvalidError{What: "status", Reason: fmt.Sprintf("%q is none of wip, review, approved, rejected", s)}
	}
	return nil
}

// cleanLabels returns labels sorted, each once, or an *InvalidError when one cannot
// be used or there are too many.
func cleanLabels(labels []string) ([]string, error) {
	for _, l := range labels {
		if !labelChars.MatchString(l) {
			return nil, &InvalidError{What: "label", Reason: fmt.Sprintf("%q can only have lowercase letters, numbers, _ and -, at most 50", l)}
		}
	}
	cleaned := slices.Compact(slices.Sorted(slices.Values(labels)))
	if len(cleaned) > MaxLabels {
		return nil, &InvalidError{What: "labels", Reason: fmt.Sprintf("a doc can have at most %d, not %d", MaxLabels, len(cleaned))}
	}
	if cleaned == nil {
		cleaned = []string{}
	}
	return cleaned, nil
}

// checkContent fails for content that is not text a doc can hold. UTF-8 lets a NUL
// through, which only a binary file has.
func checkContent(content []byte) error {
	switch {
	case len(content) > MaxContent:
		return &InvalidError{What: "content", Reason: fmt.Sprintf("it is %d bytes, more than the %d a doc can hold", len(content), MaxContent)}
	case !utf8.Valid(content):
		return &InvalidError{What: "content", Reason: "it is not UTF-8 text"}
	case bytes.IndexByte(content, 0) >= 0:
		return &InvalidError{What: "content", Reason: "it has a NUL byte, as binary files do"}
	}
	return nil
}
