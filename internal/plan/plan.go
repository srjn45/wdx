package plan

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/doc"
)

// StateDraft is the state of a plan that has not run: every plan, until plans run.
const StateDraft = "draft"

// Plan is a plan's row: what it is, without its content.
type Plan struct {
	ID          string
	ProjectID   string
	ProjectName string
	Name        string
	State       string
	Tasks       int      // how many it has; 0 for a plan of plans
	Plans       []string // the plans it lists, in DAG order; nil for a plan of tasks
	Parent      string   // the name of the plan of plans listing it; empty for none
	Revision    int      // the latest
	Size        int      // of the latest revision's text
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Revision is one revision of a plan, without its content.
type Revision = doc.Revision

// ErrNotFound is returned when no plan, or no revision of one, matches.
var ErrNotFound = errors.New("plan not found")

// InvalidError is returned for content that is not a plan, or lists plans it
// cannot.
type InvalidError struct {
	Problems []Problem
}

func (e *InvalidError) Error() string {
	list := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		list[i] = p.String()
	}
	return "invalid plan: " + strings.Join(list, "; ")
}

// NameTakenError is returned when a plan would get a name another plan in its
// project has.
type NameTakenError struct {
	Name string
	Plan Plan
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("name %q is already used by plan %s", e.Name, e.Plan.Name)
}

// ChangedError is returned when a change names a base revision that is no longer
// the plan's latest. Plan is the plan as it is now.
type ChangedError struct {
	Plan Plan
	Base int
}

func (e *ChangedError) Error() string {
	return fmt.Sprintf("plan %s is at revision %d, not %d", e.Plan.Name, e.Plan.Revision, e.Base)
}

// InUseError is returned when renaming or removing a plan a plan of plans lists:
// the parent would name a plan that is not there. wdx never edits the parent.
type InUseError struct {
	Plan   Plan
	Parent Plan
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("plan %s is listed by plan %s: take it out of %s first", e.Plan.Name, e.Parent.Name, e.Parent.Name)
}
