package plan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// waiting are the plans waiting for the person, by the id of the plan of plans
	// listing them, as its runner last saw them.
	waiting map[string][]string
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
	return &Store{db: database, docs: doc.NewKindStore(database, dir, doc.KindPlan), summaries: map[string]summary{}, waiting: map[string][]string{}}
}

// Saved is what Create and Update did: the plan, the afters reduction dropped,
// whether a revision was made, and the plans it lists whose folder became its.
type Saved struct {
	Plan    Plan
	Dropped []Dropped
	Changed bool
	Moved   []string
}

// Create adds a plan named name to the project with projectID, in folder, its
// content checked and reduced as revision 1. The plans it lists take its folder.
func (s *Store) Create(ctx context.Context, projectID, name, folder string, content []byte) (Saved, error) {
	folder, err := doc.CleanFolder(folder)
	if err != nil {
		return Saved{}, err
	}
	r, children, err := s.check(ctx, projectID, name, "", content)
	if err != nil {
		return Saved{}, err
	}
	moved, err := s.leaving(ctx, children, folder)
	if err != nil {
		return Saved{}, err
	}
	d, err := s.docs.Create(ctx, projectID, doc.New{Name: name, Folder: folder, Format: "yaml", Content: r.Content, Children: children})
	if err != nil {
		return Saved{}, s.translate(ctx, err)
	}
	p, err := s.plan(ctx, d)
	if err != nil {
		return Saved{}, err
	}
	logging.FromContext(ctx).Info("plan created", "plan_id", p.ID, "name", p.Name, "project_id", projectID, "folder", folder, "tasks", p.Tasks, "plans", len(p.Plans), "dropped", len(r.Dropped), "moved", len(moved))
	return Saved{Plan: p, Dropped: r.Dropped, Changed: true, Moved: moved}, nil
}

// Copy creates a draft named name from the latest revision of the plan with id, in
// any state, checked as Create checks a new plan; a plan of plans is copied with
// the plans it lists that are not done, as copyParent says. It goes in folder, or the plan's when
// folder is nil, and has none of the plan's history or run.
func (s *Store) Copy(ctx context.Context, id, name string, folder *string) (Saved, error) {
	from, err := s.Get(ctx, id)
	if err != nil {
		return Saved{}, err
	}
	content, _, _, err := s.Content(ctx, id, 0)
	if err != nil {
		return Saved{}, err
	}
	to := from.Folder
	if folder != nil {
		to = *folder
	}
	if from.Plans != nil {
		return s.copyParent(ctx, from, name, to, content)
	}
	saved, err := s.Create(ctx, from.ProjectID, name, to, content)
	if err != nil {
		return Saved{}, err
	}
	logging.FromContext(ctx).Info("plan copied", "plan_id", saved.Plan.ID, "name", saved.Plan.Name, "from_id", from.ID, "from_name", from.Name, "revision", from.Revision)
	return saved, nil
}

// copyParent copies the plan of plans from, of content, as name, with each plan it
// lists that is not done, as drafts in folder, to run again what is left: each
// takes its name and the first -<n> free for every one of them, and the copy lists
// only those, its afters on the plans done gone. A copy that fails halfway removes
// what it made.
func (s *Store) copyParent(ctx context.Context, from Plan, name, folder string, content []byte) (Saved, error) {
	log := logging.FromContext(ctx).With("from_id", from.ID, "from_name", from.Name)
	left, err := s.notDone(ctx, from)
	if err != nil {
		return Saved{}, err
	}
	if len(left) == 0 {
		return Saved{}, ErrAllDone
	}
	done := len(from.Plans) - len(left)
	from.Plans = left
	if taken, err := s.GetByName(ctx, from.ProjectID, name); err == nil {
		return Saved{}, &NameTakenError{Name: name, Plan: taken}
	} else if !errors.Is(err, ErrNotFound) {
		return Saved{}, err
	}
	names, err := s.freeCopyNames(ctx, from, name)
	if err != nil {
		return Saved{}, err
	}
	var made []string
	undo := func() {
		for _, id := range made {
			if _, err := s.Remove(ctx, id); err != nil {
				log.Error("half a copy left", "plan_id", id, "err", err)
			}
		}
	}
	for _, child := range from.Plans {
		id, err := s.copyChild(ctx, from.ProjectID, child, names[strings.ToLower(child)], folder)
		if err != nil {
			undo()
			return Saved{}, err
		}
		made = append(made, id)
	}
	text, err := renamed(content, names)
	if err == nil {
		var saved Saved
		if saved, err = s.Create(ctx, from.ProjectID, name, folder, text); err == nil {
			log.Info("plan of plans copied", "plan_id", saved.Plan.ID, "name", name, "plans", len(made), "done_left_out", done)
			return saved, nil
		}
	}
	undo()
	return Saved{}, err
}

// ErrAllDone is returned for copying a plan of plans whose plans are all done:
// there is nothing left to run again.
var ErrAllDone = errors.New("every plan it lists is done: nothing is left to run again")

// notDone returns the names of the plans p lists that are not done, in its order.
func (s *Store) notDone(ctx context.Context, p Plan) ([]string, error) {
	children, err := s.children(ctx, p)
	if err != nil {
		return nil, err
	}
	var left []string
	for _, c := range children {
		if c.State != StateDone {
			left = append(left, c.Name)
		}
	}
	return left, nil
}

// copyChild copies the plan called name as a draft called to in folder, and
// returns the copy's id.
func (s *Store) copyChild(ctx context.Context, projectID, name, to, folder string) (string, error) {
	c, err := s.GetByName(ctx, projectID, name)
	if err != nil {
		return "", err
	}
	text, _, _, err := s.Content(ctx, c.ID, 0)
	if err != nil {
		return "", err
	}
	saved, err := s.Create(ctx, projectID, to, folder, text)
	return saved.Plan.ID, err
}

// freeCopyNames returns the names of the copies of the plans from lists, by their
// names lowercased: each its name and -<n>, n the first from 2 free for every one,
// and none the copy's own name.
func (s *Store) freeCopyNames(ctx context.Context, from Plan, name string) (map[string]string, error) {
	for n := 2; ; n++ {
		names, free := map[string]string{}, true
		for _, child := range from.Plans {
			to := child + "-" + strconv.Itoa(n)
			_, err := s.GetByName(ctx, from.ProjectID, to)
			switch {
			case err == nil, strings.EqualFold(to, name):
				free = false
			case !errors.Is(err, ErrNotFound):
				return nil, err
			}
			names[strings.ToLower(child)] = to
		}
		if free {
			return names, nil
		}
	}
}

// leaving returns the names of the plans with ids not in folder, which a plan of
// plans listing them is about to move there.
func (s *Store) leaving(ctx context.Context, ids []string, folder string) ([]string, error) {
	moved := []string{}
	for _, id := range ids {
		d, err := s.docs.Get(ctx, id)
		if err != nil {
			return nil, s.translate(ctx, err)
		}
		if d.Folder != folder {
			moved = append(moved, d.Name)
		}
	}
	return moved, nil
}

// Move puts the plan with id in folder, in any state, with its family: a plan of
// plans and the plans it lists share one folder, whichever of them moves. It
// returns the plan, whether its folder changed, and the names of the others moved.
func (s *Store) Move(ctx context.Context, id, folder string) (Plan, bool, []string, error) {
	d, changed, moved, err := s.docs.Move(ctx, id, folder)
	if err != nil {
		return Plan{}, false, nil, s.translate(ctx, err)
	}
	p, err := s.plan(ctx, d)
	if err != nil {
		return Plan{}, false, nil, err
	}
	if moved == nil {
		moved = []string{}
	}
	if changed {
		logging.FromContext(ctx).Info("plan folder changed", "plan_id", id, "name", p.Name, "folder", p.Folder, "moved", len(moved))
	}
	return p, changed, moved, nil
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
	moved, err := s.leaving(ctx, children, d.Folder)
	if err != nil {
		return Saved{}, err
	}
	u, err := s.docs.UpdateWithChildren(ctx, id, base, r.Content, children)
	if err != nil {
		return Saved{}, s.translate(ctx, err)
	}
	if !u.Changed {
		moved = []string{} // the children come from the content, which is as it was
	}
	p, err := s.plan(ctx, u.Doc)
	if err != nil {
		return Saved{}, err
	}
	if u.Changed {
		logging.FromContext(ctx).Info("plan updated", "plan_id", p.ID, "name", p.Name, "revision", p.Revision, "tasks", p.Tasks, "plans", len(p.Plans), "dropped", len(r.Dropped), "moved", len(moved))
	}
	return Saved{Plan: p, Dropped: r.Dropped, Changed: u.Changed, Moved: moved}, nil
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

// List returns the plans of the project with projectID by folder, then name.
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
	slots, err := s.slotUses(ctx, "d.project_id = ?", projectID)
	if err != nil {
		return nil, err
	}
	plans := make([]Plan, 0, len(docs))
	for _, d := range docs {
		sum, err := s.summary(ctx, d)
		if err != nil {
			return nil, err
		}
		p := view(d, sum, names[d.ParentID], runs[d.ID])
		p.Slots = slots[d.ID]
		p.PlansWaiting = s.waitingIn(d.ID)
		plans = append(plans, p)
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
	slots, err := s.slotUses(ctx, "sl.plan_id = ?", d.ID)
	if err != nil {
		return Plan{}, err
	}
	p := view(d, sum, parent, runs[d.ID])
	p.Slots = slots[d.ID]
	p.PlansWaiting = s.waitingIn(d.ID)
	return p, nil
}

// waitingIn returns the plans the plan of plans with id lists that wait for the
// person, as its runner last saw them.
func (s *Store) waitingIn(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.waiting[id])
}

// setWaiting keeps the names of the plans the plan of plans with id lists that
// wait for the person, and reports whether they changed.
func (s *Store) setWaiting(id string, names []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(s.waiting[id], names) {
		return false
	}
	if len(names) == 0 {
		delete(s.waiting, id)
	} else {
		s.waiting[id] = names
	}
	return true
}

func view(d doc.Doc, sum summary, parent string, run runState) Plan {
	state, reason := StateDraft, ""
	if run.state != "" {
		state, reason = run.shown()
	}
	return Plan{
		ID:            d.ID,
		ProjectID:     d.ProjectID,
		ProjectName:   d.ProjectName,
		Name:          d.Name,
		Folder:        d.Folder,
		State:         state,
		PausedReason:  reason,
		TasksDone:     run.done,
		TasksFailed:   run.failed,
		PlansDone:     run.plansDone,
		projectClosed: run.projectClosed,
		ArchivedAt:    archivedAt(run),
		Tasks:         sum.tasks,
		Plans:         slices.Clone(sum.plans),
		Parent:        parent,
		Revision:      d.Revision,
		Size:          d.Size,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

// archivedAt is when run was archived, zero when it was not.
func archivedAt(run runState) time.Time {
	if !run.archivedAt.Valid {
		return time.Time{}
	}
	return time.UnixMilli(run.archivedAt.Int64).UTC()
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
