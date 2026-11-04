package plan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/srjn45/wdx/internal/doc"
	"github.com/srjn45/wdx/internal/logging"
)

// Store keeps plans as docs of the kind plan, which share docs' revisions and
// copies, and checks every revision before it is kept.
type Store struct {
	db   *sql.DB
	docs *doc.Store

	mu        sync.Mutex
	summaries map[string]summary // by plan id
}

// summary is what a plan's row shows of its latest revision's definition, kept so
// that listing plans parses each revision once, not on every change a project's
// stream sends.
type summary struct {
	projectID string
	revision  int
	tasks     int
	plans     []string
}

// NewStore returns a Store backed by database, keeping a copy of each plan's latest
// revision in dir.
func NewStore(database *sql.DB, dir string) *Store {
	return &Store{db: database, docs: doc.NewKindStore(database, dir, doc.KindPlan), summaries: map[string]summary{}}
}

// Saved is what Create and Update did: the plan, the afters reduction dropped, and
// whether a revision was made.
type Saved struct {
	Plan    Plan
	Dropped []Dropped
	Changed bool
}

// Create adds a plan named name to the project with projectID, its content
// checked and reduced as revision 1.
func (s *Store) Create(ctx context.Context, projectID, name string, content []byte) (Saved, error) {
	r, children, err := s.check(ctx, projectID, name, "", content)
	if err != nil {
		return Saved{}, err
	}
	d, err := s.docs.Create(ctx, projectID, doc.New{Name: name, Format: "yaml", Content: r.Content, Children: children})
	if err != nil {
		return Saved{}, s.translate(ctx, err)
	}
	p, err := s.plan(ctx, d)
	if err != nil {
		return Saved{}, err
	}
	logging.FromContext(ctx).Info("plan created", "plan_id", p.ID, "name", p.Name, "project_id", projectID, "tasks", p.Tasks, "plans", len(p.Plans), "dropped", len(r.Dropped))
	return Saved{Plan: p, Dropped: r.Dropped, Changed: true}, nil
}

// Update makes content, checked and reduced, the plan's next revision, if base is
// still its latest: otherwise it returns a *ChangedError. Content that reduces to
// the latest revision's changes nothing.
func (s *Store) Update(ctx context.Context, id string, base int, content []byte) (Saved, error) {
	if _, err := s.draft(ctx, id); err != nil {
		return Saved{}, err
	}
	d, err := s.docs.Get(ctx, id)
	if err != nil {
		return Saved{}, s.translate(ctx, err)
	}
	r, children, err := s.check(ctx, d.ProjectID, d.Name, d.ID, content)
	if err != nil {
		return Saved{}, err
	}
	u, err := s.docs.UpdateWithChildren(ctx, id, base, r.Content, children)
	if err != nil {
		return Saved{}, s.translate(ctx, err)
	}
	p, err := s.plan(ctx, u.Doc)
	if err != nil {
		return Saved{}, err
	}
	if u.Changed {
		logging.FromContext(ctx).Info("plan updated", "plan_id", p.ID, "name", p.Name, "revision", p.Revision, "tasks", p.Tasks, "plans", len(p.Plans), "dropped", len(r.Dropped))
	}
	return Saved{Plan: p, Dropped: r.Dropped, Changed: u.Changed}, nil
}

// check reduces content and checks the plans it lists, if any, against the project:
// each must be a plan of it, other than the one checked (self, empty for a new
// one), of tasks, and listed by no other plan. A plan another lists cannot list
// plans itself: one level only. It returns the ids of the plans listed.
func (s *Store) check(ctx context.Context, projectID, name, self string, content []byte) (Reduced, []string, error) {
	r, problems := Reduce(content)
	if problems != nil {
		logging.FromContext(ctx).Info("plan refused", "name", name, "problems", len(problems))
		return Reduced{}, nil, &InvalidError{Problems: problems}
	}
	children := r.Definition.Plans
	if children == nil {
		return r, nil, nil
	}
	if self != "" {
		d, err := s.docs.Get(ctx, self)
		if err != nil {
			return Reduced{}, nil, s.translate(ctx, err)
		}
		if d.ParentID != "" {
			parent, err := s.docs.Get(ctx, d.ParentID)
			if err != nil {
				return Reduced{}, nil, s.translate(ctx, err)
			}
			problems = append(problems, Problem{Line: children[0].Line, Message: fmt.Sprintf("%s is one of the plans of %s, so it cannot list plans: plans of plans have one level", name, parent.Name)})
		}
	}
	var ids []string
	for _, c := range children {
		if strings.EqualFold(c.ID, name) {
			problems = append(problems, Problem{Line: c.Line, Message: "a plan cannot list itself"})
			continue
		}
		d, err := s.docs.GetByName(ctx, projectID, c.ID)
		if errors.Is(err, doc.ErrNotFound) {
			problems = append(problems, Problem{Line: c.Line, Message: fmt.Sprintf("the project has no plan %s", c.ID)})
			continue
		}
		if err != nil {
			return Reduced{}, nil, err
		}
		sum, err := s.summary(ctx, d)
		if err != nil {
			return Reduced{}, nil, err
		}
		if _, err := s.draft(ctx, d.ID); err != nil {
			return Reduced{}, nil, err
		}
		switch {
		case sum.plans != nil:
			problems = append(problems, Problem{Line: c.Line, Message: fmt.Sprintf("%s lists plans itself: plans of plans have one level", d.Name)})
		case d.ParentID != "" && d.ParentID != self:
			parent, err := s.docs.Get(ctx, d.ParentID)
			if err != nil {
				return Reduced{}, nil, s.translate(ctx, err)
			}
			problems = append(problems, Problem{Line: c.Line, Message: fmt.Sprintf("%s is already one of the plans of %s: a plan has one parent", d.Name, parent.Name)})
		default:
			ids = append(ids, d.ID)
		}
	}
	if problems != nil {
		logging.FromContext(ctx).Info("plan refused", "name", name, "problems", len(problems))
		return Reduced{}, nil, &InvalidError{Problems: problems}
	}
	return r, ids, nil
}

// Get returns the plan with id.
func (s *Store) Get(ctx context.Context, id string) (Plan, error) {
	d, err := s.docs.Get(ctx, id)
	if err != nil {
		return Plan{}, s.translate(ctx, err)
	}
	return s.plan(ctx, d)
}

// GetByName returns the plan named name, ignoring case, in the project with
// projectID.
func (s *Store) GetByName(ctx context.Context, projectID, name string) (Plan, error) {
	d, err := s.docs.GetByName(ctx, projectID, name)
	if err != nil {
		return Plan{}, s.translate(ctx, err)
	}
	return s.plan(ctx, d)
}

// List returns the plans of the project with projectID by name.
func (s *Store) List(ctx context.Context, projectID string) ([]Plan, error) {
	docs, err := s.docs.List(ctx, projectID, nil, "")
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, d := range docs {
		names[d.ID] = d.Name
	}
	runs, err := s.runStates(ctx, "d.project_id = ?", projectID)
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(docs))
	for _, d := range docs {
		sum, err := s.summary(ctx, d)
		if err != nil {
			return nil, err
		}
		plans = append(plans, view(d, sum, names[d.ParentID], runs[d.ID]))
	}
	return plans, nil
}

// Content returns the text of the plan with id at revision, the latest when it is
// 0, that revision, and what it defines.
func (s *Store) Content(ctx context.Context, id string, revision int) ([]byte, Revision, Definition, error) {
	content, r, err := s.docs.Content(ctx, id, revision)
	if err != nil {
		return nil, Revision{}, Definition{}, s.translate(ctx, err)
	}
	def, problems := Parse(content)
	if problems != nil {
		return nil, Revision{}, Definition{}, fmt.Errorf("revision %d of plan %s is not a valid plan: %v", r.Number, id, problems)
	}
	return content, r, def, nil
}

// Revisions returns the revisions of the plan with id, the latest first.
func (s *Store) Revisions(ctx context.Context, id string) ([]Revision, error) {
	list, err := s.docs.Revisions(ctx, id)
	return list, s.translate(ctx, err)
}

// Rename gives the plan with id the name name, unless a plan of plans lists it.
func (s *Store) Rename(ctx context.Context, id, name string) (Plan, error) {
	p, err := s.unlisted(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	d, err := s.docs.Rename(ctx, id, name)
	if err != nil {
		return Plan{}, s.translate(ctx, err)
	}
	logging.FromContext(ctx).Info("plan renamed", "plan_id", id, "name", d.Name, "old_name", p.Name)
	return s.plan(ctx, d)
}

// Remove deletes the plan with id and its revisions, unless a plan of plans lists
// it, and returns what it was. The plans it listed are then no one's.
func (s *Store) Remove(ctx context.Context, id string) (Plan, error) {
	p, err := s.unlisted(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if _, err := s.docs.Remove(ctx, id); err != nil {
		return Plan{}, s.translate(ctx, err)
	}
	s.mu.Lock()
	delete(s.summaries, id)
	s.mu.Unlock()
	logging.FromContext(ctx).Info("plan removed", "plan_id", id, "name", p.Name, "project_id", p.ProjectID, "freed", len(p.Plans))
	return p, nil
}

// draft returns the plan with id, or a *StartedError if it has run.
func (s *Store) draft(ctx context.Context, id string) (Plan, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.State != StateDraft {
		logging.FromContext(ctx).Info("plan has run", "plan_id", id, "name", p.Name, "state", p.State)
		return Plan{}, &StartedError{Plan: p}
	}
	return p, nil
}

// unlisted returns the plan with id, a draft, or an *InUseError if a plan of plans
// lists it.
func (s *Store) unlisted(ctx context.Context, id string) (Plan, error) {
	p, err := s.draft(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.Parent == "" {
		return p, nil
	}
	parent, err := s.GetByName(ctx, p.ProjectID, p.Parent)
	if err != nil {
		return Plan{}, err
	}
	logging.FromContext(ctx).Info("plan in use", "plan_id", id, "name", p.Name, "parent", parent.Name)
	return Plan{}, &InUseError{Plan: p, Parent: parent}
}

// TaskOfAgent returns the plan and the task of its run that the agent with agentID
// does, and false when it does none.
func (s *Store) TaskOfAgent(ctx context.Context, agentID string) (planID, taskID string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT plan_id, task_id FROM plan_tasks WHERE agent_id = ? LIMIT 1`, agentID).Scan(&planID, &taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return planID, taskID, err == nil, err
}

// RemoveProjectCopies deletes the copies of the plans of the project, and what is
// kept of them, once the project is removed.
func (s *Store) RemoveProjectCopies(ctx context.Context, projectID, projectName string) {
	s.docs.RemoveProjectCopies(ctx, projectName)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sum := range s.summaries {
		if sum.projectID == projectID {
			delete(s.summaries, id)
		}
	}
}

// CatchUpCopies writes every plan's copy that is missing or older than its latest
// revision; the daemon calls it on start.
func (s *Store) CatchUpCopies(ctx context.Context) error { return s.docs.CatchUpCopies(ctx) }

// plan is d as a plan, with its parent's name.
func (s *Store) plan(ctx context.Context, d doc.Doc) (Plan, error) {
	sum, err := s.summary(ctx, d)
	if err != nil {
		return Plan{}, err
	}
	parent := ""
	if d.ParentID != "" {
		pd, err := s.docs.Get(ctx, d.ParentID)
		if err != nil {
			return Plan{}, s.translate(ctx, err)
		}
		parent = pd.Name
	}
	runs, err := s.runStates(ctx, "r.plan_id = ?", d.ID)
	if err != nil {
		return Plan{}, err
	}
	return view(d, sum, parent, runs[d.ID]), nil
}

func view(d doc.Doc, sum summary, parent string, run runState) Plan {
	state, reason := StateDraft, ""
	if run.state != "" {
		state, reason = run.shown()
	}
	return Plan{
		ID:           d.ID,
		ProjectID:    d.ProjectID,
		ProjectName:  d.ProjectName,
		Name:         d.Name,
		State:        state,
		PausedReason: reason,
		TasksDone:    run.done,
		TasksFailed:  run.failed,
		Tasks:        sum.tasks,
		Plans:        slices.Clone(sum.plans),
		Parent:       parent,
		Revision:     d.Revision,
		Size:         d.Size,
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
	}
}

// summary returns what d's latest revision defines, parsing it only when it is
// not the revision last parsed.
func (s *Store) summary(ctx context.Context, d doc.Doc) (summary, error) {
	s.mu.Lock()
	sum, ok := s.summaries[d.ID]
	s.mu.Unlock()
	if ok && sum.revision == d.Revision {
		return sum, nil
	}
	_, _, def, err := s.Content(ctx, d.ID, d.Revision)
	if err != nil {
		return summary{}, err
	}
	sum = summary{projectID: d.ProjectID, revision: d.Revision, tasks: len(def.Tasks)}
	if def.Plans != nil {
		sum.plans = make([]string, len(def.Plans))
		for i, c := range def.Plans {
			sum.plans[i] = c.ID
		}
	}
	s.mu.Lock()
	s.summaries[d.ID] = sum
	s.mu.Unlock()
	return sum, nil
}

// translate turns the doc store's errors into a plan's.
func (s *Store) translate(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, doc.ErrNotFound) {
		return fmt.Errorf("%w%s", ErrNotFound, strings.TrimPrefix(err.Error(), doc.ErrNotFound.Error()))
	}
	if e, ok := errors.AsType[*doc.NameTakenError](err); ok {
		p, perr := s.plan(ctx, e.Doc)
		if perr != nil {
			return perr
		}
		return &NameTakenError{Name: e.Name, Plan: p}
	}
	if e, ok := errors.AsType[*doc.ChangedError](err); ok {
		p, perr := s.plan(ctx, e.Doc)
		if perr != nil {
			return perr
		}
		return &ChangedError{Plan: p, Base: e.Base}
	}
	return err
}
