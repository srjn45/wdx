# Folders for docs and plans

Status: agreed with the human on 2026-11-04; designed in detail and approved on 2026-11-04 (In detail, below); step 1, storage, the API and the CLI, built on 2026-11-04; step 2, tide, built on 2026-11-04. The feature is complete. "Agreed" is what the human said; "In detail" is the design the human approved.

A project's docs and plans are one flat list, in the database, the API and the CLI. A folder is a label each of them carries, a `/`-separated path such as `/hub/initial/spec`, which tide uses to show them as a tree. It means nothing else: no logic reads it, and nothing is stored for a folder itself.

## Agreed (2026-11-04)

- **The name is `folder`**, in the column, the API, the JSON and the CLI flag (`--folder`). Not `path`: `doc create`, `plan create` and `plan update` already take `--path <file>|-` for the file to read.
- **One column on `docs`**, as a plan is a doc, so both get it at once. The migration only adds it, with the default `/`.
- **`/` by default**, which tide shows as now, at the top. `/hub` is one level down, `/hub/initial` two.
- **Cleaned, then checked**, wherever a folder is given (CLI, API, tide):
  1. clean: remove all whitespace, lowercase every letter, add a leading `/` if missing, and turn every run of `/` into one;
  2. check: only `a-z`, `0-9`, `_`, `-` and `/` are left, or it is refused.

  So `Hub //Initial` becomes `/hub/initial`, and folders are case-insensitive by being lowercase.
- **Names stay unique per project whatever the folder**, so `--name` still finds exactly one doc or plan.
- **Set and changed anytime**: `--folder` on create, and a way to change it later from the CLI (`doc set --folder`, `plan set --folder`, or on rename: to design). Also in tide, as editing it there is what a tide user would reach for.
- **The repository copy stays flat**: `docs.git_path` writes docs as now, whatever their folder.
- **A plan of plans and its children share one folder** (changed by the human on 2026-11-04, in place of "plans of plans use the folder only"): changing the folder of any of them changes it for all of them together. In tide a child stays under its parent, as now, so the folder places the family and the parent-child relation makes the level below it.
- **Built after step 3 of plans**, as a small feature of its own, so that neither grows while being built.

## In detail (approved 2026-11-04)

### The folder

- **Cleaning also drops a trailing `/`**, so a folder has one spelling: `/hub/` and `hub` are `/hub`. Anything that cleans to nothing, such as an empty `--folder ""`, is `/`.
- **One place cleans and checks**: `internal/doc`, for docs and plans alike, so the CLI, the API and tide send what was typed and all get the same answer. Refused with `invalid_argument`, whose detail names the folder and what it may hold; no new code.
- **Not part of a revision**, as labels are not: changing it makes none and needs no `--base`, and `history` does not show it. A plan's YAML says nothing of it. It can change in any state, a running or archived plan's too.
- Logged as names are, since it is a label the person types, not content.

### A plan of plans' folder

- **One folder for the family**, kept by the store, so no request can leave them apart:
  - changing the folder of a parent or of a child changes it for the parent and every child it lists, in one transaction;
  - when a parent is saved (`create` or `update`) listing a child, the child takes the parent's folder, in the transaction that writes `parent_id`;
  - a child the parent stops listing keeps the folder it has;
  - a parent created with `--folder` gives that folder to its children.
- **The answer says who moved with it**: a plan's `updated` result gains `moved`, the names of the other plans whose folder changed with it, `[]` when none. The text names them: `plan set` prints `plan sso is in /hub`, then `moved with it: auth-a, auth-b`; a parent's `create` and `update` print `moved auth-a, auth-b to /hub, as it lists them`. (The design had a count, `moved sso-login and 3 plans to /hub`; changed while building step 1, as the names say more in one line.) A parent's `create` and `update` that move children list them in `moved` too.
- Logged: `plan folder changed`, with the plan and how many moved with it.

### Storage

- `0024_doc_folders.sql`: `ALTER TABLE docs ADD COLUMN folder TEXT NOT NULL DEFAULT '/'`. No index, as nothing looks a doc up by its folder. Only an added column with a default, so not `breaks-older`: an older binary leaves it alone, and the docs it creates are at `/`.

### The API

- `Doc` and `Plan` gain `folder`, always a string, `/` at the top.
- `CreateDoc` and `CreatePlan` take an optional `folder`, `/` when absent.
- `UpdateDoc` (`PATCH /v1/docs/{doc}`) takes `folder`, with `status` and the labels; `name` still excludes them all.
- `UpdatePlan` (`PATCH /v1/plans/{plan}`) takes `folder`, alone or with `parallel`; `name` excludes both. With `parallel` on a plan not running, the whole request is `plan_not_running` and the folder is not changed either.
- `updated`, or `unchanged` when the folder cleans to the one it has. For a plan, `moved` (see A plan of plans' folder).

### The CLI

- `doc create --folder <folder>` and `plan create --folder <folder>`.
- `doc set --folder <folder>`, beside `--status` and the labels.
- `plan set --folder <folder>`: `plan set` now takes `--parallel`, `--folder` or both, at least one (a `usage` error otherwise). Its text says each: `plan sso runs up to 4 tasks at once, and is in /hub`, then `moved with it: auth-a, auth-b`.
- `doc list` and `plan list` gain a `FOLDER` column, first, and are sorted by folder, then name, so a folder's docs are together. `--json` gives `folder` on each.
- **No `--folder` filter on `list`**: `--json` and `jq` already do it, and nothing has asked for it. Easy to add later.
- `doc sync-to-git` writes the docs flat, as now.
- `plan copy` (plans step 4) copies the folder.
- `wdx doc --help` says what a folder is, as there is no `wdx help docs` topic, and `wdx help plans` says how a plan of plans shares its folder. (Changed while building step 1: the design named a `help docs` topic that does not exist.)

### tide

- **A folder is a row** in the Docs section and in the Plans section, drawn as the Code tree draws a directory: its last part (`initial`, not `/hub/initial`), the `−`/`+` fold mark, and the tree's guide lines. What is at `/` has no folder row, so a project that never sets one looks as it does now.
- **Order at each level**: folders first, by name, then the docs or plans, in the order they come now.
- **Folding as the Code tree folds**: `←` folds and `→` unfolds, a click on the mark does either, and `Enter` on a folder row toggles it. Folders start unfolded, as docs and plans are few next to files. The folds are kept per project in the view the daemon keeps (docs/TIDE_STATE.md), as the agents' are: `docs_folded` and `plans_folded`, the folders folded. As built: a click anywhere on a folder's row toggles it, as on a directory's in the Code tree; `←` on a doc in a folder, or on a plan in one that is not a plan of plans unfolded, folds that folder and puts the cursor on it; a folded folder holding the tab shown carries its `◂`.
- **A folded folder in Plans says `needs you`** when a plan in it does, as a folded agent does, so a stuck task is never hidden.
- **The filter (`/`)** matches names and labels, as now, and shows each match under its folders, unfolded while it filters; a folder's own name is not matched.
- **`m` moves** the doc or plan under the cursor: a popup with its folder to edit, as `r` renames an agent, sent as `UpdateDoc` or `UpdatePlan`. It is a line of the menu's selection part (`m move to folder`) on a doc or plan row, and works without `C-q` in the left pane. Not on a folder row: **moving a whole folder is left out**, as it would be one request per doc and not all or nothing; the person moves each, or uses the CLI in a loop. Suggested for later if wanted.
- **Plans of plans stay as now**: a child is listed under its parent, which keeps its fold mark, and the family is under the folder they share. `m` on a parent or a child moves them all, and its popup says so: `moves sso-login and its 3 plans`.

### docs/CLI_TIDE.md

- A row "A doc's or plan's folder": the CLI sets it on create, `set` and shows it in `list`; tide shows the tree and moves one with `m`, but creates no doc, so it has no create; moving a whole folder is on neither.

### Logging and tests

- `doc` store tests: the cleaning table (`Hub //Initial` → `/hub/initial`, `/hub/` → `/hub`, `""` → `/`, `a.b` refused), and that a folder change makes no revision.
- plan store tests: a folder change on a child moves its parent and siblings, a parent saved listing a child gives it its folder, a child let go keeps its own, and `moved` lists them.
- daemon: create and update with a folder, `unchanged`, `invalid_argument`, and `parallel` with `folder` on a plan not running changing nothing.
- cli: one test per outcome of `doc set --folder`, `plan set --folder`, create with `--folder`, and the `FOLDER` column's smoke test.
- tide: the tree's rows and order, folding and the fold kept in the view, `needs you` on a folded folder, the filter, `m`, and a child plan under its parent in their folder, `m` on a child moving the family.

### Steps

1. Storage, the API and the CLI.
2. tide: the tree in both sections, the folds, `m`, and a family moved together.
