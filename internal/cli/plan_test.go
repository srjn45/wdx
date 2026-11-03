package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srjn45/wdx/internal/api"
)

const planYAML = `goal: Keep sessions in the database.
tasks:
  - id: schema
    prompt: Add the table.
  - id: api
    after: [schema]
    prompt: Use it.
  - id: docs
    after: [api, schema]
    prompt: Document it.
`

// newPlan creates a plan named name from content in the project dir is in.
func newPlan(t *testing.T, home, dir, name, content string) api.Plan {
	t.Helper()
	v, exit := wdxJSONWith(t, home, both(in(dir), stdin(content)), "plan", "create", "--name", name, "--path", "-")
	if exit != api.ExitOK || v.Result != "created" || v.Plan == nil {
		t.Fatalf("create plan %s: exit %d, %+v", name, exit, v)
	}
	return *v.Plan
}

func TestPlanCreate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newProject(t, home, "lib")

	file := writeDocFile(t, "sso.yaml", planYAML)
	v, exit := wdxJSONWith(t, home, in(app), "plan", "create", "--name", "sso", "--path", file)
	if exit != api.ExitOK || v.Result != "created" || v.Plan.Name != "sso" || *v.Plan.Tasks != 3 || v.Plan.ProjectName != "app" || v.Plan.State != api.PlanStateDraft {
		t.Fatalf("from a file: exit %d, %+v", exit, v.Plan)
	}
	if len(v.Dropped) != 1 || v.Dropped[0].Entry != "docs" || v.Dropped[0].After != "schema" || v.Dropped[0].Via != "api" {
		t.Fatalf("dropped: %+v", v.Dropped)
	}
	v, exit = wdxJSONWith(t, home, both(in(app), stdin(planYAML)), "plan", "create", "--project-name", "lib", "--name", "sso", "--path", "-")
	if exit != api.ExitOK || v.Plan.ProjectName != "lib" {
		t.Fatalf("from stdin in another project: exit %d, %+v", exit, v.Plan)
	}

	v, exit = wdxJSONWith(t, home, in(app), "plan", "create", "--name", "SSO", "--path", file)
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "choose another --name, or change the plan with wdx plan update")
	v, exit = wdxJSONWith(t, home, in(app), "plan", "create", "--name", "sso_v2", "--path", file)
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	wantHint(t, v, "choose another --name")
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("goal: g\n")), "plan", "create", "--name", "bad", "--path", "-")
	wantError(t, v, exit, api.ErrorCodeInvalidPlan, api.ExitRejected)
	wantHint(t, v, "wdx help plans says how to write a plan")
	v, exit = wdxJSONWith(t, home, in(app), "plan", "create", "--name", "bad", "--path", writeDocFile(t, "binary.yaml", "\xff\xfe"))
	wantError(t, v, exit, api.ErrorCodeInvalidArgument, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "plan", "create", "--name", "x")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, both(in(t.TempDir()), stdin(planYAML)), "plan", "create", "--name", "x", "--path", "-")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "run it in a project's worktree, or pass --project-name or --project-id")
}

// TestPlanProblemsText checks the problems of a plan are printed one a line, at
// the file's line, where editors jump to them.
func TestPlanProblemsText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	file := writeDocFile(t, "bad.yaml", "goal: g\ntasks:\n  - id: a_b\n    prompt: p\n  - id: c\n")
	out := wdxWith(t, home, in(app), "plan", "create", "--name", "bad", "--path", file)
	want := "wdx: not created: the plan in " + file + " has 2 problems  [invalid_plan]\n" +
		"  " + file + ":3: id \"a_b\" can only have letters, numbers and -, at most 50\n" +
		"  " + file + ":5: task c has no prompt\n" +
		"  hint: wdx help plans says how to write a plan\n"
	if out.exit != api.ExitRejected || !strings.HasPrefix(out.stderr, want) {
		t.Fatalf("got %q\nwant it to start with %q", out.stderr, want)
	}
	out = wdxWith(t, home, both(in(app), stdin("goal: g\n")), "plan", "create", "--name", "bad", "--path", "-")
	if !strings.Contains(out.stderr, "the plan in stdin has 1 problem  [invalid_plan]\n  stdin:1: no tasks or plans\n") {
		t.Fatalf("from stdin: %q", out.stderr)
	}
	newPlan(t, home, app, "sso", planYAML)
	out = wdxWith(t, home, both(in(app), stdin("goal: g\n")), "plan", "update", "--name", "sso", "--path", "-", "--base", "1")
	if !strings.Contains(out.stderr, "wdx: not updated: the plan in stdin has 1 problem  [invalid_plan]\n  stdin:1: no tasks or plans\n") {
		t.Fatalf("an update: %q", out.stderr)
	}
}

func TestPlanGet(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	p := newPlan(t, home, app, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")

	v, exit := wdxJSONWith(t, home, in(app), "plan", "get", "--name", "sso")
	if exit != api.ExitOK || !strings.HasPrefix(*v.Content, "goal: g") || *v.Revision != 1 || v.Plan.Id != p.Id {
		t.Fatalf("the latest: exit %d, %+v", exit, v)
	}
	copy := filepath.Join(t.TempDir(), "sso.yaml")
	v, exit = wdxJSONWith(t, home, in(app), "plan", "get", "--id", p.Id, "--out", copy)
	if exit != api.ExitOK || v.Content != nil {
		t.Fatalf("--out: exit %d, %+v", exit, v)
	}
	if b, err := os.ReadFile(copy); err != nil || !strings.HasPrefix(string(b), "goal: g") {
		t.Fatalf("the copy: %q, %v", b, err)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "get", "--name", "sso", "--out", copy)
	wantError(t, v, exit, api.ErrorCodePathExists, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "plan", "get", "--name", "sso", "--revision", "2")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "plan", "get", "--name", "sso", "--overwrite")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "plan", "get", "--name", "sso")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
	wantHint(t, v, "names are unique within a project: run it in the plan's project, or pass --project-name or --project-id")
}

func TestPlanUpdate(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newPlan(t, home, app, "sso", "goal: g\ntasks:\n  - id: a\n    prompt: p\n")

	v, exit := wdxJSONWith(t, home, both(in(app), stdin(planYAML)), "plan", "update", "--name", "sso", "--path", "-", "--base", "1")
	if exit != api.ExitOK || v.Result != "updated" || v.Plan.Revision != 2 || *v.Plan.Tasks != 3 || len(v.Dropped) != 1 {
		t.Fatalf("updated: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, both(in(app), stdin(planYAML)), "plan", "update", "--name", "sso", "--path", "-", "--base", "2")
	if exit != api.ExitOK || v.Result != "unchanged" {
		t.Fatalf("the same YAML: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, both(in(app), stdin(planYAML)), "plan", "update", "--name", "sso", "--path", "-", "--base", "1")
	wantError(t, v, exit, api.ErrorCodePlanChanged, api.ExitRejected)
	wantHint(t, v, "get it again with wdx plan get --name sso --out <file> --overwrite, apply your change, and update with --base 2")
	v, exit = wdxJSONWith(t, home, both(in(app), stdin("tasks: []\n")), "plan", "update", "--name", "sso", "--path", "-", "--base", "2")
	wantError(t, v, exit, api.ErrorCodeInvalidPlan, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, both(in(app), stdin(planYAML)), "plan", "update", "--name", "nowhere", "--path", "-", "--base", "1")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestPlanListAndHistory(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	v, exit := wdxJSONWith(t, home, in(app), "plan", "list")
	if exit != api.ExitOK || v.Plans == nil || len(v.Plans) != 0 {
		t.Fatalf("none: exit %d, %+v", exit, v)
	}
	newPlan(t, home, app, "sso", planYAML)
	newDoc(t, home, app, "notes", "")
	v, exit = wdxJSONWith(t, home, in(app), "plan", "list")
	if exit != api.ExitOK || len(v.Plans) != 1 || v.Plans[0].Name != "sso" {
		t.Fatalf("one: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(t.TempDir()), "plan", "list")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)

	v, exit = wdxJSONWith(t, home, in(app), "plan", "history", "--name", "sso")
	if exit != api.ExitOK || len(v.Revisions) != 1 || v.Revisions[0].Number != 1 {
		t.Fatalf("history: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "history", "--name", "notes")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestPlanStatus(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newPlan(t, home, app, "sso", planYAML)
	v, exit := wdxJSONWith(t, home, in(app), "plan", "status", "--name", "sso")
	if exit != api.ExitOK || v.Definition == nil || v.Definition.Goal != "Keep sessions in the database." || len(*v.Definition.Tasks) != 3 || (*v.Definition.Tasks)[2].Id != "docs" {
		t.Fatalf("status: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "status", "--name", "nowhere")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestPlansOfPlansCommands(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newPlan(t, home, app, "auth-a", planYAML)
	newPlan(t, home, app, "auth", "goal: g\nplans:\n  - id: auth-a\n")

	v, exit := wdxJSONWith(t, home, in(app), "plan", "rename", "--name", "auth-a", "--new-name", "auth-one")
	wantError(t, v, exit, api.ErrorCodePlanInUse, api.ExitRejected)
	wantHint(t, v, "take it out of auth first: wdx plan get --name auth --out <file>, then wdx plan update")
	v, exit = wdxJSONWith(t, home, in(app), "plan", "remove", "--name", "auth-a", "--yes")
	wantError(t, v, exit, api.ErrorCodePlanInUse, api.ExitRejected)
	wantHint(t, v, "take it out of auth first: wdx plan get --name auth --out <file>, then wdx plan update")

	out := wdxWith(t, home, in(app), "plan", "status", "--name", "auth")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "auth: draft, 1 plan\n") || !strings.Contains(out.stdout, "PLAN") || !strings.Contains(out.stdout, "auth-a") {
		t.Fatalf("the parent's status: %+v", out)
	}
	out = wdxWith(t, home, in(app), "plan", "status", "--name", "auth-a")
	if out.exit != api.ExitOK || !strings.Contains(out.stdout, "parent") {
		t.Fatalf("the child's status: %+v", out)
	}
}

func TestPlanRename(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newPlan(t, home, app, "sso", planYAML)
	newPlan(t, home, app, "auth", planYAML)

	v, exit := wdxJSONWith(t, home, in(app), "plan", "rename", "--name", "sso", "--new-name", "sso-login")
	if exit != api.ExitOK || v.Result != "updated" || v.Plan.Name != "sso-login" {
		t.Fatalf("renamed: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "rename", "--name", "sso-login", "--new-name", "sso-login")
	if exit != api.ExitOK || v.Result != "unchanged" {
		t.Fatalf("its own name: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "rename", "--name", "sso-login", "--new-name", "AUTH")
	wantError(t, v, exit, api.ErrorCodeNameTaken, api.ExitRejected)
	wantHint(t, v, "choose another --new-name")
	v, exit = wdxJSONWith(t, home, in(app), "plan", "rename", "--name", "sso-login", "--new-name", "a_b")
	wantError(t, v, exit, api.ErrorCodeInvalidName, api.ExitRejected)
	v, exit = wdxJSONWith(t, home, in(app), "plan", "rename", "--name", "nowhere", "--new-name", "x")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestPlanRemove(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	p := newPlan(t, home, app, "sso", planYAML)
	newPlan(t, home, app, "auth", planYAML)

	v, exit := wdxJSONWith(t, home, in(app), "plan", "remove", "--name", "sso")
	wantError(t, v, exit, api.ErrorCodeUsage, api.ExitUsage)
	out := wdxWith(t, home, in(app), "plan", "remove", "--name", "sso")
	if out.exit != api.ExitUsage || !strings.Contains(out.stderr, "pass --yes") {
		t.Fatalf("without a terminal or --yes: %+v", out)
	}
	out = wdxWith(t, home, both(in(app), answering(t, "n")), "plan", "remove", "--name", "sso")
	if out.exit != api.ExitRejected || !strings.Contains(out.stderr, "remove plan sso and its 1 revision? [y/N]: ") || !strings.Contains(out.stderr, "[cancelled]") {
		t.Fatalf("answered no: %+v", out)
	}
	out = wdxWith(t, home, both(in(app), answering(t, "y")), "plan", "remove", "--name", "sso")
	if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, "plan sso removed") {
		t.Fatalf("answered yes: %+v", out)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "remove", "--name", "auth", "--yes")
	if exit != api.ExitOK || v.Result != "removed" || v.Plan.Name != "auth" {
		t.Fatalf("--yes: exit %d, %+v", exit, v)
	}
	v, exit = wdxJSONWith(t, home, in(app), "plan", "remove", "--id", p.Id, "--yes")
	wantError(t, v, exit, api.ErrorCodeNotFound, api.ExitRejected)
}

func TestPlanText(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	file := writeDocFile(t, "sso.yaml", planYAML)

	for _, c := range []struct {
		args   []string
		stdout string
	}{
		{[]string{"plan", "create", "--name", "sso", "--path", file}, "plan sso created, with 3 tasks\ndropped docs after schema: api already waits for it\n"},
		{[]string{"plan", "list"}, "NAME"},
		{[]string{"plan", "status", "--name", "sso"}, "sso: draft, 3 tasks\n  goal        Keep sessions in the database.\n"},
		{[]string{"plan", "update", "--name", "sso", "--path", writeDocFile(t, "one.yaml", "goal: g\ntasks:\n  - id: a\n    prompt: p\n"), "--base", "1"}, "sso is at revision 2, with 1 task\n"},
		{[]string{"plan", "update", "--name", "sso", "--path", writeDocFile(t, "same.yaml", "goal: g\ntasks:\n  - id: a\n    prompt: p\n"), "--base", "2"}, "sso is unchanged at revision 2\n"},
		{[]string{"plan", "history", "--name", "sso"}, "REVISION"},
		{[]string{"plan", "get", "--name", "sso"}, "goal: g\n"},
		{[]string{"plan", "get", "--name", "sso", "--out", filepath.Join(t.TempDir(), "a.yaml")}, "revision 2 of sso written to "},
		{[]string{"plan", "rename", "--name", "sso", "--new-name", "sso-login"}, "plan sso renamed to sso-login\n"},
		{[]string{"plan", "remove", "--name", "sso-login", "--yes"}, "plan sso-login removed\n"},
		{[]string{"plan", "list"}, "no plans\n"},
	} {
		out := wdxWith(t, home, in(app), c.args...)
		if out.exit != api.ExitOK || !strings.HasPrefix(out.stdout, c.stdout) || out.stderr != "" {
			t.Errorf("%v: want stdout from %q, got %+v", c.args, c.stdout, out)
		}
	}
}

// TestPlanTextNotLogged checks the cli measures a plan's YAML and the goal and
// prompts of its definition in what it logs, never logging them.
func TestPlanTextNotLogged(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	newPlan(t, home, app, "sso", "goal: goal-secret\ntasks:\n  - id: a\n    prompt: prompt-secret\n")
	for _, args := range [][]string{{"plan", "get", "--name", "sso"}, {"plan", "status", "--name", "sso"}} {
		if out := wdxWith(t, home, in(app), args...); out.exit != api.ExitOK || !strings.Contains(out.stdout, "-secret") {
			t.Fatalf("%v: %+v", args, out)
		}
		if _, exit := wdxJSONWith(t, home, in(app), args...); exit != api.ExitOK {
			t.Fatalf("%v --json: exit %d", args, exit)
		}
	}
	logs, err := filepath.Glob(filepath.Join(home, "logs", "*.log"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("no logs: %v", err)
	}
	for _, path := range logs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "-secret") {
			t.Errorf("a plan's text is logged in %s", path)
		}
	}
}

// TestPlansTopicExamples creates the examples of wdx help plans, so that they
// cannot go stale.
func TestPlansTopicExamples(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	for _, name := range []string{"auth-sessions", "auth-tokens", "auth-cleanup"} {
		newPlan(t, home, app, name, planTasksExample)
	}
	if p := newPlan(t, home, app, "auth", planPlansExample); p.Plans == nil || len(*p.Plans) != 3 {
		t.Fatalf("the plan of plans: %+v", p)
	}
}

func TestPlanCompletions(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	app := newProjectDir(t, home, "app")
	p := newPlan(t, home, app, "sso", planYAML)
	newPlan(t, home, app, "auth", "goal: g\nplans:\n  - id: sso\n")
	if _, exit := wdxJSONWith(t, home, both(in(app), stdin(planYAML+"  - id: more\n    prompt: p\n")), "plan", "update", "--name", "sso", "--path", "-", "--base", "1"); exit != api.ExitOK {
		t.Fatalf("update: exit %d", exit)
	}
	newDoc(t, home, app, "notes", "")
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"plan", "get", "--name", ""}, []string{"auth\t1 plan, ", "sso\t4 tasks, " + p.Id}},
		{[]string{"plan", "status", "--id", ""}, []string{p.Id + "\tsso in app"}},
		{[]string{"plan", "get", "--name", "sso", "--revision", ""}, []string{"1\t", "2\t"}},
	} {
		out := wdxWith(t, home, in(app), append([]string{"__complete"}, tc.args...)...)
		for _, want := range tc.want {
			if out.exit != api.ExitOK || !strings.Contains(out.stdout, want) || strings.Contains(out.stdout, "notes") {
				t.Errorf("%v: want %q, got %+v", tc.args, want, out)
			}
		}
	}
}
