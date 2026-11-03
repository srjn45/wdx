# Docs

Status: approved by the human on 2026-11-02; steps 1 to 6 built. "Agreed" is what the human said; "Design" came from the AI and was approved. docs/IDE.md has the wider picture.

## Why

Before AI, teams kept docs elsewhere: Google Docs, Jira, Confluence. AIs need to read them, so teams now commit docs to the repository. In bigger firms even a doc change needs a PR and an approval, and until it merges another engineer working on something close does not know about it.

wdx keeps docs outside the repository, so that a doc is there for everyone, people and AIs, as soon as it is written. A doc's status tells an AI what is agreed and what is not.

Once on wdx, docs are written and changed only through `wdx doc`, as an AI works through a Google Docs CLI or Notion's MCP. Adding an existing file is only how docs made elsewhere come in. As every change goes through wdx, wdx sees each one and can later sync it to a remote server, which is not designed now and does not shape this design.

## Agreed

On 2026-11-02:

- A doc belongs to a project. Every doc command takes the project flags.
- The database is where a doc lives: a row per doc, and a row per revision holding the whole content. Every change is a new revision.
- A client changes a doc on a copy of its own and writes it back naming the revision it read. If the doc has moved on since, the write fails, and the client reads the doc again, applies its change and writes again. So AIs use their own file tools, and two agents never overwrite each other.
- wdx also keeps a read-only copy of each doc's latest revision in its home, unknown to the user, to recover from if the database is lost.
- `wdx doc sync-to-git` writes the docs, read-only, into the project's repository once. A setting has the daemon keep that copy up to date on every change, as it does the hidden one. Every copy on disk is read-only, so that no AI or person edits one by accident.
- Statuses: `wip`, `review`, `approved`, `rejected`. Changing a doc's content sets it to `wip`, with a warning. Approvals by peers will be of a revision, not of the doc, and come with the hub.
- Names and labels: a name is the file's name without its extension when not given; labels have no spaces and no capitals; several labels narrow a list, as a doc must have them all.
- Content is UTF-8 text, mostly markdown.
- tide only shows docs, read-only. No editor on a doc for now.
- `remove` asks for confirmation as other removals do, with a flag that answers it.
- No author on a revision while wdx is local: there is one user. With the hub, revisions, comments and approvals record the user who made them.
- Search through docs waits for a later version.

Later, not designed: the hub; keeping only one's own docs locally and reading shared ones from the hub; approvals and comments; search; plans, DAGs in a wdx-defined YAML made from approved docs.

## Design

### Names, labels and content

- A name is `A-Z a-z 0-9 _ -`, at most 100 characters, unique in its project whatever its case. `_` is allowed so that `api_design.md` comes in as it is.
- A name taken from a file's name that a name cannot be fails with `invalid_name`, hinting at `--name`; it is never changed quietly.
- A doc has a `format`, its file extension (`md`, `txt`, …): what tide highlights it by and what the copies on disk are named with. `create --path` takes it from the file, `A-Z a-z 0-9` and at most 10 characters, or none; `--format` sets it, and is how a doc from stdin gets one. It defaults to `md`.
- A label is `a-z 0-9 _ -`, at most 50 characters. A capital is refused with `invalid_argument` rather than lowered, so that `Auth` and `auth` never both seem to exist. At most 20 labels; one given twice counts once.
- Content is valid UTF-8 with no NUL byte, at most 2 MiB, the limit tide's file tab has. A NUL passes UTF-8 but marks a binary file. A failure is `invalid_argument`, saying which. An empty doc is allowed.

### The tables

```sql
CREATE TABLE docs (
	id         TEXT PRIMARY KEY,                -- doc-…
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	name       TEXT NOT NULL COLLATE NOCASE,
	format     TEXT NOT NULL,                   -- '' or 'md'
	labels     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(labels)),
	status     TEXT NOT NULL DEFAULT 'wip' CHECK (status IN ('wip', 'review', 'approved', 'rejected')),
	revision   INTEGER NOT NULL,                -- the latest
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE (project_id, name)
) STRICT;

CREATE TABLE doc_revisions (
	doc_id     TEXT NOT NULL REFERENCES docs (id) ON DELETE CASCADE,
	revision   INTEGER NOT NULL,                -- 1, 2, …
	content    BLOB NOT NULL,                   -- gzip of the text
	size       INTEGER NOT NULL,                -- bytes of the text
	created_at INTEGER NOT NULL,
	PRIMARY KEY (doc_id, revision)
) STRICT;
```

- Revisions are numbered per doc from 1, and kept until the doc is removed.
- Content is compressed with gzip, from Go's standard library: text shrinks four to five times, which keeps the database and the backup `wdx update` takes of it small. A heavy user's 2,000 docs of 30 revisions of 20 KB come to about 300 MB.
- A status added later rebuilds `docs` to change the `CHECK`, which also rebuilds `doc_revisions` through its foreign key; that is fine in a migration.

### Writing a change

- `update` names its base revision. In one transaction: if the doc's latest is the base, the new content becomes revision base + 1 and the status `wip`; otherwise it fails with a new code, `doc_changed` (409), its details holding the latest revision.
- Content equal to the latest revision makes no revision and keeps the status: result `unchanged`.
- Metadata (status, labels, name) changes the `docs` row only, never a revision, and needs no base: two such changes cannot lose content.

### The copies on disk

- Hidden: `<home>/docs/<project>/<name>.<format>`, the latest revision, mode 0444. `<project>` is the project's name escaped as in a URL path (`/` is `%2F`, a leading `.` is `%2E`), since a project's name may hold almost anything; usual names are left as they are, so the files still say which project each belongs to.
- The daemon writes a doc's copy after the transaction that changes it commits, whole or not at all (`internal/atomicfile`, then `chmod`). A copy that fails to write is logged and fails nothing: the database holds the doc. On start, the daemon writes any copy that is missing or older than its doc, so a copy lost to a crash comes back. A copy's modified time is its revision's, which is how it is known to be current without reading it. Catching up never deletes a copy, even one no doc matches: were the database lost or replaced by an empty one, the copies are all that is left. Only removing a doc or its project deletes copies, so a crash between a removal and its file leaves a copy behind, to be deleted by hand.
- Rename and remove move or delete the copy; removing a project deletes its directory; a future `project rename` must move the directory in the same step as the row.
- Recovering from the copies is done by hand, not by a command, until it is needed.

### The git copy

- `wdx doc sync-to-git --path <dir>` writes every doc of the project into `<dir>`, a directory relative to the project's repository, in its main checkout, read-only, once. It also deletes files there of docs that no longer exist, and only those it wrote: wdx keeps a list of them in `<dir>/.wdx-docs`, so a file a person put there is never touched.
- The setting `docs.git_path`, empty by default: a directory relative to each project's repository. When set, the daemon writes there on every change, as it does the hidden copy. One key rather than an `enabled` and a `path`, since empty means off; settings are global, so it applies to every project.
- One way: what changes in the git copy is never read back, and the next write puts the doc back. wdx writes the files and nothing else; it never stages or commits, as background git must never write the index.
- `--path` and the setting are relative to the project's main checkout, wherever the command runs: the one path on the command line not made absolute against the working directory, since the setting, which applies to every project, can mean nothing else, and the two agree. An absolute path, one leading out of the checkout through `..` or a symlink, or one into `.git`, is `invalid_path`. Approved on 2026-11-03, with the four below.
- A file there of a doc's name that wdx did not write is never touched: that doc is skipped, logged, and listed under `skipped`.
- `.wdx-docs` lists the files wdx wrote, one name per line, sorted, read-only, written last; it is committed with the docs, so a clone knows which files wdx owns.
- Files of docs that are gone are removed first, then every doc is written: on a filesystem that ignores case, writing `Auth.md` and then removing `auth.md` would delete the doc just written. Every doc is written on every sync: a project has few, and an unchanged one gets the same bytes.
- With the setting, the daemon syncs a doc's whole project after each change to one, once it has committed, and every project when it starts with the setting or a reload changes it, so that a path newly set is not empty until a doc changes. A failure is logged and fails nothing.
- Read-only protects the local copy only: git records no write permission but the executable bit, so a clone gets ordinary files.
- A project whose checkout is missing skips the write, logged.

### The API

| Method and path | Does | Result |
|---|---|---|
| `POST /v1/docs` | creates a doc | 201 `created` |
| `GET /v1/docs?project=&dir=&labels=&status=` | a project's docs, by name | `docs` |
| `GET /v1/docs/{doc}?project=&dir=&revision=` | a doc with a revision's content, the latest by default | `doc`, `revision`, `content` |
| `PUT /v1/docs/{doc}/content` | a new revision, with `base` | 200 `updated` or `unchanged` |
| `PATCH /v1/docs/{doc}` | status, labels, name | 200 `updated` or `unchanged` |
| `GET /v1/docs/{doc}/revisions` | its revisions: number, size, time | `revisions` |
| `DELETE /v1/docs/{doc}?project=&dir=` | removes a doc with its revisions and copies | 200 `removed` |
| `POST /v1/docs/sync-to-git` | writes the project's git copy once, with `path`: `path` (absolute), `written`, `removed` and `skipped` file names | 200 `synced` |

- `{doc}` is a name, or `id:` and an id, as `{project}` is. The project comes from `project` or `dir`, as for terminals and agents; listing and creating need one of them, as docs belong to a project.
- `labels` in a list is repeated (`labels=a&labels=b`), as the generated client sends an array; a label no doc could have, as one with a capital, is `invalid_argument` rather than matching nothing.
- `PATCH` takes `name`, or `status`, `add_labels` and `remove_labels`, never both: a rename and a status are separate transactions, and together one could be made and the other fail. Asking for nothing is `usage`; a label both added and removed is `invalid_argument`.
- Content travels in the requests, never a path: the daemon never reads a client's files, and a client never learns where the copies are.
- A doc: `id`, `project_id`, `project_name`, `name`, `format`, `labels`, `status`, `revision`, `size` (of the latest), `created_at`, `updated_at`. An `updated` content change also returns `previous_status`.
- `Project` gains `docs`, without content, so that the project stream tells tide of every change.
- Docs work in a closed project too: nothing about them runs.
- Errors: `not_found` (a doc, or a revision it does not have), `name_taken` (the doc under `details.doc`), `invalid_name`, `invalid_argument`, `doc_changed` (the doc as it is now under `details.doc`, whose `revision` is the latest, and `details.base`), and for `sync-to-git` `checkout_missing` and `invalid_path` (a path leading out of the repository).
- Logs name the doc, project, revision and size of every change, never the content.

### The CLI

Each command takes `--project-id <id> | --project-name <name>`, or the project of the working directory, and a doc is named by `--id` or `--name`.

| Command | Text output |
|---|---|
| `doc create --name <name> [--path <file> \| --path -] [--format <ext>] [--labels <a,b>] [--status <status>]` | `doc auth-rfc created` |
| `doc get (--id \| --name) [--revision <n>] [--out <file> [--overwrite]]` | the content alone, byte for byte, like `cat`; with `--out`, `revision 4 of auth-rfc written to <file>` |
| `doc update (--id \| --name) --path <file>\|- --base <n>` | `auth-rfc is at revision 5`, and on stderr `auth-rfc was approved; it is wip again` when it was not `wip`; `auth-rfc is unchanged at revision 5` |
| `doc list [--labels <a,b>] [--status <status>]` | a table: `NAME`, `STATUS`, `REVISION`, `LABELS`, `UPDATED` |
| `doc history (--id \| --name)` | a table: `REVISION`, `SIZE`, `CREATED` |
| `doc set (--id \| --name) [--status <status>] [--add-labels <a,b>] [--remove-labels <a,b>]` | `doc auth-rfc is approved, with labels auth, rfc`; `is already approved` when unchanged |
| `doc rename (--id \| --name) --new-name <name>` | `doc auth-rfc renamed to auth-design` |
| `doc remove (--id \| --name) [--yes]` | `doc auth-rfc removed` |
| `doc sync-to-git --path <dir>` | `12 docs written to docs/wdx` |

- `create` without `--path` makes an empty doc, and needs `--name`, as `--path -` does. A name the project has fails with `name_taken`: replacing a doc's content is `update`, which needs a base.
- `get --out` writes the file read-write, as it is the client's own copy to edit, and refuses an existing file with `path_exists`, hint `pass --overwrite to replace it`; `--overwrite` replaces it. No state says which files `get` wrote: a flag is simpler, and never loses a person's file by accident. `--json` returns `{"doc": {...}, "revision": 4, "content": "..."}`, without the content when `--out` is given.
- `update` on `doc_changed`: `auth-rfc is at revision 6, not 4`, hint `get it again with wdx doc get --name auth-rfc --out <file> --overwrite, apply your change, and update with --base 6`.
- `create` and `update` refuse a file that is not UTF-8 themselves, with `invalid_argument`: the request's JSON would quietly replace what is not.
- `remove` asks `remove doc auth-rfc and its 6 revisions? [y/N]` on a terminal, and `n` is `cancelled`; `--yes` answers it; `--json`, or no terminal, requires `--yes` (`usage`).
- `--path -` reads stdin. Restoring a revision is `get --revision 3 --out f`, then `update --path f --base <latest>`; there is no `restore` or `diff`.
- Completions: `--name` the project's docs, `--labels` and `--remove-labels` its labels, `--status` the four statuses, `--revision` and `--base` the doc's revisions.

### tide

- A Docs section in the left pane, letter `d` in `tide.section_order`, whose default becomes `awcd` so that Docs comes after what is there now. A value already set leaves Docs after the letters it names.
- A row per doc: name, status (`approved` as success, `rejected` as failure, `wip` and `review` muted), labels muted. The left pane's `/` filter matches a doc's name or labels.
- `Enter` opens a `doc: auth-rfc` tab in the file tab's read-only view: highlighted by `format`, line numbers, `/` search. Its title shows the revision and status. It is read again when the stream says the doc changed.
- Nothing else in tide changes a doc.

Approved on 2026-11-03, as built:

- The rows are the project's docs from its stream, by name; nothing more is asked of the daemon. The heading counts them. With none, the row says `no docs: see wdx doc create`, which fits the pane's usual width. A narrow row drops the labels first, then the status whole, as a word cut short would say another.
- The filter matches a doc when its name and labels together hold every word typed.
- `Enter` or a click opens the tab, and the focus stays in the list, as for a file, unless the list is zoomed.
- The tab is the doc's id (`doc:<id>`), so a rename keeps it and changes its title: `doc: auth-rfc r4 approved`. It shows the latest revision, read with `GetDoc`, and is read again only when the stream brings a new revision, not for a status or labels, nor on coming back to the tab.
- An empty doc says `an empty doc`. A doc removed closes its tab and the status line says `doc auth-rfc removed`. A daemon that sends no docs closes none.
- The tab has the file tab's keys but editing: the bar and `?` leave out `e`, and `e` does nothing.
- Not in tide: older revisions, history, and every change to a doc, which the CLI makes.

### docs/CLI_TIDE.md

A Docs table. Reading a doc: Same. Create, update, status and labels, rename, remove, history, sync-to-git: By design, CLI only, since AIs write docs and tide only shows them for now.

### Steps

Each builds and passes `make test` on its own.

1. **Done.** The migration and `internal/doc`: names, labels, content checks, revisions and the base check, with domain tests.
2. **Done.** The hidden copies: writing after each change, and `CatchUpCopies` and `RemoveProjectCopies`, which step 3 has the daemon call on start and on removing a project.
3. **Done.** The API and daemon: spec, `make generate`, handlers, error mapping, `Project.docs` and the stream, removing a project.
4. **Done.** The CLI: the commands but `sync-to-git`, help, examples, completions, integration tests.
5. **Done.** The git copy: `sync-to-git` and the `docs.git_path` setting.
6. **Done.** tide: the Docs section and its tab, `tide.section_order`.
7. docs/CLI_TIDE.md, README, and `wdx help agents` once it exists, so that agents know to read and change docs through `wdx doc`.
