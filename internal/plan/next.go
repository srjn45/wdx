package plan

// AgentState is what the runner sees of a task's agent.
type AgentState int

// The states of a task's agent, as Next tells them apart.
const (
	// AgentEnded ended by itself, did not start, was lost or was removed.
	AgentEnded AgentState = iota
	AgentLive
	// AgentStopped was stopped with its project, which starts it again when it
	// opens: its task goes on then.
	AgentStopped
)

// Facts is what Next decides from: a run, and what the runner found of the world
// around it.
type Facts struct {
	Run         Run
	Parallel    int // in force: the run's, else the setting's
	ProjectOpen bool
	Landing     bool                  // a landing of the run's is going
	Agents      map[string]AgentState // the agents of its tasks, by id; one missing has ended
	SlotsGone   map[int]bool          // the slots whose directory is not on disk
}

// ActionKind is what an Action does.
type ActionKind string

// The actions, in the order Next gives them.
const (
	ActionDropSlot ActionKind = "drop_slot" // forget a slot that is gone and held by no work
	ActionFail     ActionKind = "fail"      // a running task, with Failure
	ActionStop     ActionKind = "stop"      // the live agent of a cancelled run's task
	ActionRelease  ActionKind = "release"   // a slot the run no longer lends: kept if it holds work, else freed or removed
	ActionClose    ActionKind = "close"     // a cancelled run, once its agents and slots are seen to
	ActionLand     ActionKind = "land"      // a landing task, onto the run's branch
	ActionStart    ActionKind = "start"     // a pending task, in Slot, or a new one when 0
	ActionFinish   ActionKind = "finish"    // the run, every task done or skipped
)

// Action is one thing the runner is to do.
type Action struct {
	Kind    ActionKind
	Task    string // for a release, the task holding Slot, if any
	Slot    int
	Failure string
}

// Next says what to do about a run now, in order; the runner does the first and
// asks again. Nothing to do is nil. It only reads f, so that every case is a test
// with no process.
func Next(f Facts) []Action {
	r := f.Run
	if r.State == StateCancelled && r.FinishedAt.IsZero() {
		return cancelling(f)
	}
	if r.State != StateRunning && r.State != StatePaused {
		return nil
	}
	var actions []Action
	holders := map[int]TaskRun{}
	for _, t := range r.Tasks {
		if t.Slot != 0 {
			holders[t.Slot] = t
		}
	}
	failed := map[string]bool{}
	var free []int
	for _, sl := range r.Slots {
		t, held := holders[sl.Number]
		switch {
		case !f.SlotsGone[sl.Number]:
			if !held {
				free = append(free, sl.Number)
			}
		case !held || t.State == TaskPending:
			actions = append(actions, Action{Kind: ActionDropSlot, Slot: sl.Number})
		case t.State == TaskRunning:
			actions = append(actions, Action{Kind: ActionFail, Task: t.ID, Failure: FailureSlotMissing})
			failed[t.ID] = true
		}
	}
	for _, t := range r.Tasks {
		if t.State != TaskRunning || failed[t.ID] {
			continue
		}
		if state, ok := f.Agents[t.AgentID]; t.AgentID == "" || !ok || state == AgentEnded {
			actions = append(actions, Action{Kind: ActionFail, Task: t.ID, Failure: FailureAgentEnded})
			failed[t.ID] = true
		}
	}
	for _, t := range r.Tasks {
		if t.State == TaskSkipped && t.Slot != 0 {
			actions = append(actions, Action{Kind: ActionRelease, Task: t.ID, Slot: t.Slot})
		}
	}
	// A landing needs the project open for its check's terminal, and goes one at a
	// time, so that each rebases onto the last.
	for _, t := range r.Tasks {
		if t.State == TaskLanding && f.ProjectOpen && !f.Landing {
			actions = append(actions, Action{Kind: ActionLand, Task: t.ID})
			break
		}
	}
	if f.ProjectOpen && r.State == StateRunning {
		actions = append(actions, starts(r, f.Parallel, failed, free)...)
	}
	finished := true
	for _, t := range r.Tasks {
		if t.State != TaskDone && t.State != TaskSkipped {
			finished = false
		}
	}
	// A paused run finishes too: it has nothing left to hold back.
	if finished && !f.Landing {
		actions = append(actions, Action{Kind: ActionFinish})
	}
	return actions
}

// cancelling returns what is left of cancelling a run: once no landing is going,
// stop its tasks' live agents, then release every slot, then close it. Tasks keep
// the states they had.
func cancelling(f Facts) []Action {
	if f.Landing {
		return nil
	}
	var actions []Action
	for _, t := range f.Run.Tasks {
		if t.AgentID != "" && f.Agents[t.AgentID] == AgentLive {
			actions = append(actions, Action{Kind: ActionStop, Task: t.ID})
		}
	}
	holders := map[int]string{}
	for _, t := range f.Run.Tasks {
		if t.Slot != 0 {
			holders[t.Slot] = t.ID
		}
	}
	for _, sl := range f.Run.Slots {
		actions = append(actions, Action{Kind: ActionRelease, Task: holders[sl.Number], Slot: sl.Number})
	}
	return append(actions, Action{Kind: ActionClose})
}

// starts returns the starts of r's pending tasks: first those a start cut short
// left holding a slot, then, in DAG order, those whose afters are all done or
// skipped, while fewer than parallel tasks are at work, each in the lowest free
// slot or a new one.
func starts(r Run, parallel int, failed map[string]bool, free []int) []Action {
	var actions []Action
	busy := 0
	for _, t := range r.Tasks {
		switch {
		case failed[t.ID]:
		case t.State == TaskPending && t.Slot != 0:
			actions = append(actions, Action{Kind: ActionStart, Task: t.ID, Slot: t.Slot})
			busy++
		case t.State == TaskRunning || t.State == TaskLanding:
			busy++
		}
	}
	states := map[string]string{}
	for _, t := range r.Tasks {
		states[t.ID] = t.State
	}
	afters := map[string][]string{}
	for _, t := range r.Definition.Tasks {
		afters[t.ID] = t.After
	}
	for _, t := range r.Tasks {
		if busy >= parallel {
			break
		}
		if t.State != TaskPending || t.Slot != 0 || !ready(afters[t.ID], states) {
			continue
		}
		slot := 0
		if len(free) > 0 {
			slot, free = free[0], free[1:]
		}
		actions = append(actions, Action{Kind: ActionStart, Task: t.ID, Slot: slot})
		busy++
	}
	return actions
}

func ready(after []string, states map[string]string) bool {
	for _, a := range after {
		if states[a] != TaskDone && states[a] != TaskSkipped {
			return false
		}
	}
	return true
}
