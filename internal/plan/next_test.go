package plan

import (
	"reflect"
	"testing"
)

// diamond is a run of a, then b and c, then d after both, every task pending and
// no slot made.
func diamond() Run {
	def := Definition{Tasks: []Task{{ID: "a"}, {ID: "b", After: []string{"a"}}, {ID: "c", After: []string{"a"}}, {ID: "d", After: []string{"b", "c"}}}}
	r := Run{State: StateRunning, Definition: def}
	for _, t := range def.Tasks {
		r.Tasks = append(r.Tasks, TaskRun{ID: t.ID, State: TaskPending})
	}
	return r
}

// with returns r with the task id in state, holding slot and run by agent.
func with(r Run, id, state string, slot int, agent string) Run {
	tasks := append([]TaskRun(nil), r.Tasks...)
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].State, tasks[i].Slot, tasks[i].AgentID = state, slot, agent
		}
	}
	r.Tasks = tasks
	return r
}

func withSlots(r Run, numbers ...int) Run {
	r.Slots = nil
	for _, n := range numbers {
		r.Slots = append(r.Slots, Slot{Number: n})
	}
	return r
}

func TestNext(t *testing.T) {
	t.Parallel()
	start := func(task string, slot int) Action { return Action{Kind: ActionStart, Task: task, Slot: slot} }
	fail := func(task, failure string) Action { return Action{Kind: ActionFail, Task: task, Failure: failure} }
	land := func(task string) Action { return Action{Kind: ActionLand, Task: task} }
	drop := func(slot int) Action { return Action{Kind: ActionDropSlot, Slot: slot} }
	finish := Action{Kind: ActionFinish}

	aDone := with(diamond(), "a", TaskDone, 0, "")
	allDone := with(with(with(aDone, "b", TaskDone, 0, ""), "c", TaskDone, 0, ""), "d", TaskDone, 0, "")
	paused := func(r Run) Run { r.State = StatePaused; return r }
	state := func(r Run, s string) Run { r.State = s; return r }

	tests := []struct {
		name     string
		run      Run
		parallel int
		closed   bool
		agents   map[string]AgentState
		gone     []int
		want     []Action
	}{
		{name: "the first task in a new slot", run: diamond(), parallel: 3, want: []Action{start("a", 0)}},
		{name: "what it unblocked, the free slot first", run: withSlots(aDone, 1), parallel: 3, want: []Action{start("b", 1), start("c", 0)}},
		{name: "no more than parallel", run: withSlots(aDone, 1), parallel: 1, want: []Action{start("b", 1)}},
		{name: "the lowest free slot", run: withSlots(aDone, 3, 2), parallel: 1, want: []Action{start("b", 3)}},
		{name: "skipped unblocks", run: with(diamond(), "a", TaskSkipped, 0, ""), parallel: 2, want: []Action{start("b", 0), start("c", 0)}},
		{name: "a live agent is waited for", run: withSlots(with(diamond(), "a", TaskRunning, 1, "ag"), 1), parallel: 3, agents: map[string]AgentState{"ag": AgentLive}},
		{name: "an agent stopped with its project is waited for", run: withSlots(with(diamond(), "a", TaskRunning, 1, "ag"), 1), parallel: 3, agents: map[string]AgentState{"ag": AgentStopped}},
		{name: "an agent that ended", run: withSlots(with(diamond(), "a", TaskRunning, 1, "ag"), 1), parallel: 3, agents: map[string]AgentState{"ag": AgentEnded}, want: []Action{fail("a", FailureAgentEnded)}},
		{name: "an agent not found", run: withSlots(with(diamond(), "a", TaskRunning, 1, "ag"), 1), parallel: 3, want: []Action{fail("a", FailureAgentEnded)}},
		{name: "an agent removed", run: withSlots(with(diamond(), "a", TaskRunning, 1, ""), 1), parallel: 3, agents: map[string]AgentState{"": AgentLive}, want: []Action{fail("a", FailureAgentEnded)}},
		{name: "a slot gone under a running task", run: withSlots(with(diamond(), "a", TaskRunning, 1, "ag"), 1), parallel: 3, gone: []int{1}, want: []Action{fail("a", FailureSlotMissing)}},
		{name: "a free slot gone", run: withSlots(diamond(), 1), parallel: 3, gone: []int{1}, want: []Action{drop(1), start("a", 0)}},
		{name: "a slot gone under a start cut short", run: withSlots(with(diamond(), "a", TaskPending, 1, ""), 1), parallel: 3, gone: []int{1}, want: []Action{drop(1), start("a", 1)}},
		{name: "a slot gone under a failed task is kept", run: withSlots(with(aDone, "b", TaskFailed, 1, ""), 1), parallel: 1, gone: []int{1}, want: []Action{start("c", 0)}},
		{name: "a task failing now is not at work", run: withSlots(with(aDone, "b", TaskRunning, 1, "ag"), 1), parallel: 1, want: []Action{fail("b", FailureAgentEnded), start("c", 0)}},
		{name: "a start cut short goes first", run: with(aDone, "c", TaskPending, 1, ""), parallel: 1, want: []Action{start("c", 1)}},
		{name: "a failed task holds its slot but is not at work", run: withSlots(with(aDone, "b", TaskFailed, 1, ""), 1), parallel: 1, want: []Action{start("c", 0)}},
		{name: "a land failed task holds its slot", run: withSlots(with(aDone, "b", TaskLandFailed, 1, ""), 1, 2), parallel: 2, want: []Action{start("c", 2)}},
		{name: "a landing task is at work", run: withSlots(with(aDone, "b", TaskLanding, 1, ""), 1), parallel: 1, want: []Action{land("b")}},
		{name: "one landing at a time", run: withSlots(with(with(aDone, "b", TaskLanding, 1, ""), "c", TaskLanding, 2, ""), 1, 2), parallel: 3, want: []Action{land("b")}},
		{name: "a failed after holds what waits for it", run: with(with(aDone, "b", TaskFailed, 0, ""), "c", TaskDone, 0, ""), parallel: 3},
		{name: "paused starts nothing", run: paused(aDone), parallel: 3},
		{name: "paused still lands and fails", run: paused(with(with(aDone, "b", TaskLanding, 1, ""), "c", TaskRunning, 2, "ag")), parallel: 3, want: []Action{fail("c", FailureAgentEnded), land("b")}},
		{name: "a closed project starts nothing", run: aDone, parallel: 3, closed: true},
		{name: "a closed project still lands", run: with(aDone, "b", TaskLanding, 1, ""), parallel: 3, closed: true, want: []Action{land("b")}},
		{name: "finished", run: allDone, parallel: 3, want: []Action{finish}},
		{name: "finished with a skipped task", run: with(allDone, "c", TaskSkipped, 0, ""), parallel: 3, want: []Action{finish}},
		{name: "a free slot gone, then finished", run: withSlots(allDone, 1), parallel: 3, gone: []int{1}, want: []Action{drop(1), finish}},
		{name: "paused does not finish", run: paused(allDone), parallel: 3},
		{name: "a run done", run: state(aDone, StateDone), parallel: 3},
		{name: "a run cancelled", run: state(with(aDone, "b", TaskLanding, 1, ""), StateCancelled), parallel: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gone := map[int]bool{}
			for _, n := range tc.gone {
				gone[n] = true
			}
			got := Next(Facts{Run: tc.run, Parallel: tc.parallel, ProjectOpen: !tc.closed, Agents: tc.agents, SlotsGone: gone})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
