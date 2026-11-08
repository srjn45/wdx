package cli

import (
	"fmt"
	"strings"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

// topics are wdx's help topics.
var topics = []clikit.Topic{
	{Name: "json", Summary: "what --json prints: results, errors and the help itself", Text: jsonTopic},
	{Name: "settings", Summary: "every setting, its default and where it is read from", Text: settingsTopic, Settings: config.Settings()},
	{Name: "logs", Summary: "where runs are logged, and finding one by its debug_id", Text: logsTopic},
	{Name: "agents", Summary: "how an AI agent wdx runs works through wdx: child agents, messages, worktrees, docs, plans", Text: agentsTopic},
	{Name: "plans", Summary: "how to write a plan: its YAML, its rules, and examples", Text: plansTopic},
}

func jsonTopic(st style.Style) string {
	return `With ` + st.Flag("--json") + `, nothing prompts and stdout holds exactly one JSON object and
nothing else, the same the daemon's API returns. The flags that answer a
command's questions are then required up front.

  success: {"result": "created", "project": {...}}
  failure: Problem Details (RFC 9457):
           {"type": "urn:wdx:error:not_found", "title": "...", "status": 404,
            "detail": "...", "code": "not_found", "hint": "...",
            "details": {...}, "debug_id": "dbg-..."}

"result" names what happened; commands that change nothing have none. Keys
are snake_case, absent values null and timestamps RFC 3339. Each command's
help lists its results and error codes, and ` + usageText(st, identity.Name+" <command> --help --json") + `
prints the help itself as JSON.`
}

func settingsTopic(st style.Style) string {
	var b strings.Builder
	b.WriteString(`Read when ` + identity.Name + ` starts; an invalid one fails every command. The home is
read from the environment, the rest from <home>/config.yaml, where
log.level is written as "log: {level: debug}". ` + st.Command(identity.Name+" setting list") + ` shows them,
and ` + st.Command(identity.Name+" setting set") + ` changes one and has the daemon apply it at once; after
editing the file by hand, run ` + st.Command(identity.Name+" daemon restart") + `. ` + st.Command(identity.Name+" setting write") + `
lists every setting in the file, as ` + st.Command(identity.Name+" update") + ` does with a new release's.

`)
	b.WriteString(clikit.SettingsTable(st, config.Settings()))
	b.WriteString(`
` + st.Flag("--no-input") + ` and ` + st.Flag("--color") + ` override no_input and color for one command.
NO_COLOR set to anything, or TERM=dumb, turns color off where it is auto.`)
	return b.String()
}

func logsTopic(st style.Style) string {
	return `Every run of ` + identity.Name + ` and every request to the daemon is logged as JSON
lines to <home>/logs/` + identity.Name + `-<date>.log, kept for log.retention_days days.

A failure prints its debug_id; every line of that run, in ` + identity.Name + ` and in the
daemon, carries it. ` + usageText(st, identity.Name+" observe start") + ` shows the logs in Grafana, where
the debug_id finds them too.`
}

// agentsTopic is the manual of an AI agent wdx runs (docs/IDE.md, "MCP and
// skills"): workflows and when to use each, not flags. Every line indented by four
// spaces is a command line, checked by TestAgentsTopicCommands; prose is
// indented by two.
func agentsTopic(st style.Style) string {
	w := identity.Name
	commands := func(rows ...string) string {
		var table style.Table
		for i := 0; i < len(rows); i += 2 {
			if rows[i+1] == "" {
				table.Row("    " + st.Command(rows[i]))
			} else {
				table.Row("    "+st.Command(rows[i]), st.Muted(rows[i+1]))
			}
		}
		var b strings.Builder
		table.Write(&b)
		return b.String()
	}
	heading := func(s string) string { return st.Heading(s) + "\n" }
	return `How an AI coding agent that ` + w + ` runs works through it. Run each command with
` + st.Flag("--json") + `; ` + st.Command(w+" <command> --help --json") + ` says the rest.

` + heading("You") + `  ` + w + ` sets WDX_AGENT_ID in your environment.
` + commands(w+" agent get --id $WDX_AGENT_ID", "your project, worktree, parent and children") + `
` + heading(w+" comes first") + `  Use ` + w + ` rather than your own means for what it has, so that the person sees
  all the work in one place, whichever AI runs it: child agents for subagents,
  their worktrees for yours, ` + st.Command(w+" agent send") + ` to talk to another agent, and
  ` + st.Command(w+" doc") + ` for docs. If ` + w + ` fails, use your own means and say so.

` + heading("Child agents") + commands(
		w+" agent create --parent-id $WDX_AGENT_ID --name <name> --prompt <task>", "",
		w+" agent create --parent-id $WDX_AGENT_ID --name <name> --worktree <name> --prompt <task>", "",
	) + `  Put your id in a child's task, and ask it to report back with
  ` + st.Command(w+" agent send") + ` when it is done.
` + commands(
		w+" agent get --name <name>", "what it is doing: working, idle or waiting",
		w+" agent list", "",
	) + `
` + heading("Messages") + commands(
		w+" agent send --name <name> --prompt <text>", "delivered once it can take it",
		w+" agent send --name <name> --prompt <text> --instant", "first; Claude Code takes it at once",
		w+" message list", "those still waiting",
	) + `
` + heading("Worktrees") + commands(
		w+" worktree list", "every worktree and what is uncommitted",
		w+" worktree diff --name <worktree>", "what it has committed",
	) + `  Landing and removing a worktree are the person's to do, unless you are asked.
  When asked to land or rebase, say it is you, or you count as working there:
` + commands(
		w+" worktree land --name <worktree> --agent-id $"+identity.Env("AGENT_ID"), "",
		w+" worktree rebase --name <worktree> --agent-id $"+identity.Env("AGENT_ID"), "",
	) + `
` + heading("Docs") + `  A project's docs live in ` + w + `, not in the repository: read and change them
  only through ` + st.Command(w+" doc") + `, never through a copy in the repository. A doc's
  status says what is agreed: approved is, wip and review are not, rejected
  was turned down.
` + commands(
		w+" doc list", "",
		w+" doc get --name <doc>", "",
	) + `  To change one, get a copy, edit it with your own tools, and write it back
  naming the revision you read:
` + commands(
		w+" doc get --name <doc> --out <file>", "says the revision, here 4",
		w+" doc update --name <doc> --path <file> --base 4", "",
	) + `  ` + st.Name("doc_changed") + ` means another wrote first: get it again with ` + st.Flag("--overwrite") + `, apply
  your change, and update with the new base.
` + commands(w+" doc create --name <doc> --path <file> --labels <labels>", "") +
		`  Approving or rejecting a doc is the person's to do, unless you are asked.

` + heading("Plans") + `  A plan breaks a feature into tasks for agents, each with the tasks it waits
  for, or a big plan into smaller ones. ` + st.Command(w+" help plans") + ` says how to write one.
  Send it on stdin, with no file to write:
` + commands(w+" plan create --name <plan> --path -", "") + `  ` + st.Name("invalid_plan") + ` lists every problem with its line: fix them all and send
  it again. To change a plan, as a doc, get a copy and write it back naming
  the revision you read:
` + commands(
		w+" plan get --name <plan> --out <file>", "says the revision, here 2",
		w+" plan update --name <plan> --path <file> --base 2", "",
		w+" plan status --name <plan>", "its tasks in the order they run",
	) + `  Running a plan is the person's to do, unless you are asked.

  When you are told you are doing a task of a plan, commit your work on the
  branch you are on, then say it is done, with what you did in a few lines,
  which the tasks after yours are told:
` + commands(w+" plan task done --agent-id $"+identity.Env("AGENT_ID")+" --summary <text>", "") + `  ` + st.Name("not_committed") + ` and ` + st.Name("wrong_branch") + ` say what to fix first. Then stop: ` + w + ` stops you,
  and lands your work on the plan's branch.`
}

// The examples of wdx help plans, which TestPlansTopicExamples creates.
const (
	planTasksExample = `goal: Keep sessions in the database instead of cookies.
constraints:
  - The public API does not change.
tasks:
  - id: schema
    prompt: |
      Add a sessions table with id, user_id and expires_at.
  - id: store
    after: [schema]
    prompt: |
      Write the session store on top of the table.
  - id: api
    after: [schema]
    prompt: |
      Change the login and logout handlers to use sessions.
  - id: docs
    after: [store, api]
    prompt: |
      Document the session lifetime in docs/AUTH.md.
`
	planPlansExample = `goal: Rewrite authentication.
plans:
  - id: auth-sessions
  - id: auth-tokens
    after: [auth-sessions]
  - id: auth-cleanup
    after: [auth-tokens]
`
)

// plansTopic is the reference of a plan's YAML (docs/PLANS.md).
func plansTopic(st style.Style) string {
	w := identity.Name
	indent := func(text string) string {
		var b strings.Builder
		for line := range strings.Lines(text) {
			b.WriteString("    " + line)
		}
		return b.String()
	}
	key := func(name, text string) string { return "  " + st.Name(fmt.Sprintf("%-12s", name)) + " " + text + "\n" }
	return `A plan is YAML, checked whenever it is saved: every problem is reported with its
line, and nothing is saved until there are none.

` + key("goal", "required: what the work is for; every task's agent is told it") +
		key("constraints", "a list of rules every task's agent is told") +
		key("pr", "true: "+w+" opens the plan's PR once it is done; false if left out") +
		key("stacked", "a plan of plans only, with pr: true: true to stack its plans' PRs") +
		key("tasks", "the tasks, each:") +
		key("  id", "letters, numbers and -, at most 50, unique whatever the case") +
		key("  prompt", "what the task's agent is asked to do") +
		key("  after", "a list of the tasks it waits for") +
		key("plans", "in place of tasks, for a plan of plans: plans of the project, each:") +
		key("  id", "the plan's name") +
		key("  after", "a list of the plans it waits for") + `
A task with no after starts at once: nothing waits unless after says so. Tasks
are listed in DAG order, after everything they wait for, keeping the order they
are written in where they are free to go in either.

An after that another path already covers is dropped when the plan is saved:
with docs after store and schema, and store after schema, docs' after schema
goes. The plan is stored without it, keeping comments, and ` + w + ` says what it
dropped.

` + st.Heading("A plan of tasks") + `
` + indent(planTasksExample) + `
` + st.Heading("A plan of plans") + `
  A big plan broken into smaller ones, each landing as a PR of its own. It
  lists plans of tasks the project already has, never plans of plans, and a
  plan has one parent at most. A plan a plan of plans lists cannot be renamed
  or removed until it is taken out. It shares its parent's folder, where TIDE
  shows them: those it lists move to the parent's when saved, and plan set
  --folder on any of them moves them all.
` + indent(planPlansExample) + `
` + st.Heading("Saving one") + `
  Send the YAML on stdin, with no file to write:
    ` + st.Command(w+" plan create --name sso --path - <<'EOF'") + `
    ...
    ` + st.Command("EOF") + `
  To change one, get a copy and write it back naming the revision you read:
    ` + st.Command(w+" plan get --name sso --out sso.yaml") + `
    ` + st.Command(w+" plan update --name sso --path sso.yaml --base 1") + `

` + st.Heading("Running one") + `
  ` + st.Command(w+" plan run --name sso") + ` makes the branch plan/sso from the default branch,
  and starts an agent for every task whose afters are done, each in a worktree
  of the plan's, up to plan.parallel at once (--parallel for this run). Each
  agent is told the goal, the constraints, the summaries of the tasks it waits
  for, and its prompt. Once its work is committed, it says so:
    ` + st.Command(w+" plan task done --agent-id $"+identity.Env("AGENT_ID")+" --summary <text>") + `
  ` + w + ` then stops it and rebases its work onto plan/sso, removes the agent,
  and what waited for it starts; the task keeps its state and summary, with
  no agent. Landing runs the project's landing check first, if it has one.
    ` + st.Command(w+" plan status --name sso") + `       every task's state
    ` + st.Command(w+" plan task get --plan sso --task <id>") + `   one task's prompt and summary
  Once every task is done, one worktree is left on plan/sso, to land as any.
  Cancelling a plan removes the agents left, as a task's agent is kept only
  while its task can be retried.
  A plan runs once, and cannot be changed once it has. To run it again, or
  start from it, copy it to a draft; one that is over can be archived, out
  of plan list until --all:
    ` + st.Command(w+" plan copy --name sso --new-name sso-2") + `   then plan run --name sso-2
    ` + st.Command(w+" plan archive --name sso") + `                plan unarchive brings it back

` + st.Heading("When a task cannot go on") + `
  A task is failed when its agent ended before saying it was done, did not
  start, or lost its worktree; land failed when its rebase onto plan/sso
  stopped on conflicts, or the check failed, its output kept in a terminal.
  What waits for it waits for you, while the rest goes on:
    ` + st.Command(w+" plan task retry --plan sso --task <id>") + `   its agent resumes, told what went wrong
    ` + st.Command(w+" plan task retry ... --fresh") + `             a new agent, told of the earlier commits
    ` + st.Command(w+" plan task land --plan sso --task <id>") + `    you dealt with it in its worktree yourself
    ` + st.Command(w+" plan task done --plan sso --task <id> --summary <text>") + `   you finished a failed one
    ` + st.Command(w+" plan task skip --plan sso --task <id>") + `    what waits for it goes on without it, its agent removed
  A task keeps its worktree and commits through all of these.
    ` + st.Command(w+" plan pause --name sso") + `  nothing new starts; ` + st.Command("resume") + ` goes on
    ` + st.Command(w+" plan set --name sso --parallel <n>") + `   more or fewer tasks at once
    ` + st.Command(w+" plan cancel --name sso") + `  stops it for good, keeping every worktree with work
  Closing the project pauses its plans, and opening it resumes them.

` + st.Heading("Running a plan of plans") + `
    ` + st.Command(w+" plan run --name auth") + `
  starts each plan it lists from the default branch once the plans before it
  are done and their work is in the default branch, so each lands as a PR of
  its own. ` + w + ` sees work landed through it (` + st.Command(w+" worktree land") + `), or a PR merged
  and pulled. A plan whose work it cannot see waits for you; running it is
  your answer, which also asks gh whether the PR before it is merged:
    ` + st.Command(w+" plan status --name auth") + `      waiting for you, or for the plans not done
    ` + st.Command(w+" plan run --name auth-b") + `       once the PR of auth-a is merged and pulled
  ` + w + ` fetches nothing: pull the default branch first. Pausing, resuming,
  cancelling and archiving a plan of plans reach its plans; copying it copies
  those not done, to run again what is left.

` + st.Heading("Its PRs, and stacking them") + `
  With pr: true, once a plan is done ` + w + ` lands its worktree by PR, as
  ` + st.Command(w+" worktree land --method pr") + ` does: the check, the push, gh pr create. A
  landing that fails leaves the plan done, its row saying why. Set on a plan
  of plans, it is for every plan it lists, which cannot set pr themselves.
  With stacked: true as well, a plan waiting for a single plan starts as soon
  as that one is done, on its branch: auth-b's plan/auth-b from plan/auth-a,
  its diff and PR against plan/auth-a, so that a review does not hold up the
  next. A plan waiting for several, or for one landed, starts from the default
  branch. Once the PR below has moved on or merged, rebase the worktree on
  the plan's branch, from the bottom of the stack up:
    ` + st.Command(w+" worktree rebase --name <worktree>") + `
  asks gh about the PR below: not merged, it takes that plan's new commits;
  merged and pulled, it moves onto the default branch and gh pr edit moves
  its PR's base there; merged but not pulled, base_not_pulled says to pull.
  Only the plan's own commits move, with git rebase --onto. A branch on origin
  is pushed again with --force-with-lease, refused if anything else was
  pushed there. A conflict or uncommitted changes stop it with nothing
  changed: rebase_failed and not_committed list the files, for you or an
  agent to deal with.`
}
