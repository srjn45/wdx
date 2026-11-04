package cli

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Manage docs: a project's text, kept by wdx outside its repository",
		Long: "A doc belongs to a project and is there for every person and agent as soon\n" +
			"as it is written. Every change of its content is a new revision, made on the\n" +
			"revision it was read at: get a copy with doc get --out, change it, and write\n" +
			"it back with doc update --base. Its status says whether it is agreed on:\n" +
			"wip, review, approved or rejected; changing its content makes it wip.\n\n" +
			"Its folder, such as /hub/initial, is only where tide shows it, / for the\n" +
			"top: a folder given is cleaned (whitespace dropped, lowercased, one / between\n" +
			"its parts and at its start) and then has a-z, 0-9, _, - and / only. Names\n" +
			"stay unique within a project whatever the folder.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDocCreateCmd(), newDocGetCmd(), newDocHistoryCmd(), newDocListCmd(), newDocRemoveCmd(),
		newDocRenameCmd(), newDocSetCmd(), newDocSyncToGitCmd(), newDocUpdateCmd())
	return cmd
}

// docStatuses are the statuses a doc can have, as --status completes them.
var docStatuses = []string{string(api.DocStatusWip), string(api.DocStatusReview), string(api.DocStatusApproved), string(api.DocStatusRejected)}

func addFolderFlag(cmd *cobra.Command, folder *string, usage string) {
	cmd.Flags().StringVar(folder, "folder", "", usage)
	completeFlag(cmd, "folder", cobra.NoFileCompletions)
}

func addStatusFlag(cmd *cobra.Command, status *string, usage string) {
	cmd.Flags().StringVar(status, "status", "", usage)
	completeFlag(cmd, "status", cobra.FixedCompletions(docStatuses, cobra.ShellCompDirectiveNoFileComp))
}

// docRef is a doc named on the command line by --id, or by --name in a project.
type docRef struct {
	id, name string
	scope    projectScope
}

func (r *docRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.id, "id", "", "`id` of the doc")
	cmd.Flags().StringVar(&r.name, "name", "", "`name` of the doc (case-insensitive)")
	cmd.MarkFlagsOneRequired("id", "name")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
	completeFlag(cmd, "id", completeDocs("id"))
	completeFlag(cmd, "name", completeDocs("name"))
	r.scope.addFlags(cmd)
}

// resolve returns how the API names the doc: its path, and the project or dir
// that names its project, if any.
func (r docRef) resolve(cmd *cobra.Command) (path, project, dir string, err error) {
	if r.id != "" {
		return "id:" + r.id, r.scope.ref(), "", nil
	}
	if r.scope.given() {
		return r.name, r.scope.ref(), "", nil
	}
	dir, err = workDir(cmd)
	return r.name, "", dir, err
}

// flag is how the command line named the doc, for a hint to repeat.
func (r docRef) flag() string {
	if r.id != "" {
		return "--id " + r.id
	}
	return "--name " + r.name
}

// notFoundHint tells where to look for a doc that was not found.
func (r docRef) notFoundHint(err error) error {
	if problemCode(err) != api.ErrorCodeNotFound || r.id != "" || r.scope.given() {
		return err
	}
	return &hinted{err, "names are unique within a project: run it in the doc's project, or pass --project-name or --project-id"}
}

func newDocCreateCmd() *cobra.Command {
	var scope projectScope
	var name, path, format, status, folder string
	var labels []string
	cmd := &cobra.Command{
		Use:   "create [--project-id <id> | --project-name <name>] [--name <name>] [--path <file> | --path -] [--folder <folder>] [--format <ext>] [--labels <a,b>] [--status <status>]",
		Short: "Create a doc, from a file or empty",
		Long: "Its content is the file --path names, stdin with --path -, or nothing. The\n" +
			"name is the file's without its extension unless --name is given, and is\n" +
			"required without a file; names have letters, numbers, _ and - only, and are\n" +
			"unique within a project whatever their case. The format, the extension its\n" +
			"copies get, is the file's, else md. Labels have lowercase letters, numbers,\n" +
			"_ and - only. Content is UTF-8 text of at most 2 MiB.\n\n" +
			"A name the project has is refused: changing a doc's content is doc update.",
		Example: examples(
			"Add a file of the project you are in as a doc", identity.Name+" doc create --path docs/auth.md",
			"Name it, label it and pick the project", identity.Name+" doc create --project-name app --path notes.md --name auth-rfc --labels auth,rfc",
			"In a folder", identity.Name+" doc create --path docs/auth.md --folder /hub/initial",
			"From stdin, as JSON", identity.Name+" doc create --name auth-rfc --path - --status review --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not created",
			outputNote:    "The doc, at revision 1.\n" + `With --json: {"result": "created", "doc": {...}}`,
			resultList:    "created",
			errorList: codeList(api.ErrorCodeInvalidArgument, api.ErrorCodeInvalidName, api.ErrorCodeNameTaken, api.ErrorCodeNotFound,
				api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := api.CreateDoc{Name: name, Labels: optionalSlice(labels), Status: optionalStatus(status)}
			if cmd.Flags().Changed("folder") {
				body.Folder = &folder
			}
			if cmd.Flags().Changed("format") {
				body.Format = &format
			}
			fromFile := path != "" && path != "-"
			if !fromFile && name == "" {
				return &usageError{errors.New("--name is required without a file to take it from")}
			}
			if fromFile {
				base := filepath.Base(path)
				ext := filepath.Ext(base)
				if name == "" {
					body.Name = strings.TrimSuffix(base, ext)
				}
				if body.Format == nil {
					body.Format = ptr(strings.TrimPrefix(ext, "."))
				}
			}
			if path != "" {
				content, err := readContent(cmd, path)
				if err != nil {
					return err
				}
				body.Content = &content
			}
			project, dir, err := docScope(cmd, scope)
			if err != nil {
				return err
			}
			body.Project, body.Dir = optional(project), optional(dir)
			r, err := clientOf(cmd).CreateDoc(cmd.Context(), body)
			if err != nil {
				return createDocHint(outsideHint(err, scope, ""), name == "")
			}
			return printResult(cmd, docResult(r))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "`name` of the doc (default: the file's, without its extension)")
	cmd.Flags().StringVar(&path, "path", "", "`file` to take the content from, - for stdin (default: empty)")
	cmd.Flags().StringVar(&format, "format", "", "`extension` of its copies, such as md or txt (default: the file's, else md)")
	cmd.Flags().StringSliceVar(&labels, "labels", nil, "`labels` to give it, comma-separated")
	addStatusFlag(cmd, &status, "`status` to create it in: wip, review, approved or rejected (default: wip)")
	addFolderFlag(cmd, &folder, "`folder` tide shows it in, such as /hub/initial (default: /, the top)")
	completeFlag(cmd, "name", cobra.NoFileCompletions)
	completeFlag(cmd, "path", completeFiles)
	completeFlag(cmd, "format", cobra.NoFileCompletions)
	completeFlag(cmd, "labels", completeDocLabels)
	return cmd
}

// createDocHint tells what to do about the errors creating a doc can return;
// named says the name came from the file.
func createDocHint(err error, named bool) error {
	switch problemCode(err) {
	case api.ErrorCodeNameTaken:
		return &hinted{err, "choose another --name, or change the doc with " + identity.Name + " doc update"}
	case api.ErrorCodeInvalidName:
		if named {
			return &hinted{err, "the name comes from the file: pass --name"}
		}
		return &hinted{err, "choose another --name"}
	}
	return err
}

// docScope is how the API names the project of a doc command: the flags, else
// the working directory.
func docScope(cmd *cobra.Command, scope projectScope) (project, dir string, err error) {
	if scope.given() {
		return scope.ref(), "", nil
	}
	dir, err = workDir(cmd)
	return "", dir, err
}

// readContent reads the file path names, or stdin for -. Text that is not
// UTF-8 is refused here: the request's JSON would quietly replace what is not.
func readContent(cmd *cobra.Command, path string) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(cmd.InOrStdin())
	} else {
		if path, err = absolute(cmd, path); err != nil {
			return "", err
		}
		b, err = os.ReadFile(path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "", &usageError{fmt.Errorf("no file %s", path)}
	}
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeInvalidArgument, "invalid content: it is not UTF-8 text")}
	}
	logging.FromContext(cmd.Context()).Info("content read", "path", path, "size", len(b))
	return string(b), nil
}

func newDocGetCmd() *cobra.Command {
	var ref docRef
	var revision int64
	var out string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--revision <n>] [--out <file> [--overwrite]]",
		Short: "Print a doc's content, or write it to a file to change",
		Long: "The latest revision unless --revision is given, printed byte for byte, as\n" +
			"cat does. With --out it is written to a file of yours to edit, then given\n" +
			"back with doc update --base and the revision it was read at; a file there is\n" +
			"replaced only with --overwrite.",
		Example: examples(
			"Print a doc of the project you are in", identity.Name+" doc get --name auth-rfc",
			"Write it to a file to change", identity.Name+" doc get --name auth-rfc --out auth-rfc.md",
			"Get it again after it changed", identity.Name+" doc get --name auth-rfc --out auth-rfc.md --overwrite",
			"An earlier revision, as JSON", identity.Name+" doc get --id doc-1a2b3c4d --revision 3 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The content alone; with --out, the revision written and where.\n" +
				`With --json: {"doc": {...}, "revision": ..., "content": "..."}, without content with --out`,
			errorList: codeList(api.ErrorCodeNotFound, api.ErrorCodePathExists, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if overwrite && out == "" {
				return &usageError{errors.New("--overwrite is for --out")}
			}
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if out, err = absolute(cmd, out); err != nil {
				return err
			}
			d, err := clientOf(cmd).GetDoc(cmd.Context(), path, project, dir, revision)
			if err != nil {
				return ref.notFoundHint(err)
			}
			r := docContent{Doc: d.Doc, Revision: d.Revision}
			if out == "" {
				r.Content = &d.Content
				return printResult(cmd, r)
			}
			if err := writeCopy(cmd.Context(), out, d.Content, overwrite); err != nil {
				return err
			}
			r.out = out
			return printResult(cmd, r)
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().Int64Var(&revision, "revision", 0, "the `revision` to get (default: the latest)")
	cmd.Flags().StringVar(&out, "out", "", "`file` to write it to, read-write, instead of printing it")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "with --out, replace the file if there is one")
	completeFlag(cmd, "revision", completeDocRevisions)
	completeFlag(cmd, "out", completeFiles)
	return cmd
}

// writeCopy writes content to path, refusing a file there unless overwrite.
func writeCopy(ctx context.Context, path, content string, overwrite bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !overwrite {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, fs.ErrExist) {
		p := api.NewProblem(api.ErrorCodePathExists, path+" already exists")
		p.Details = &map[string]any{"path": path}
		return &hinted{&client.ProblemError{Problem: p}, "pass --overwrite to replace it"}
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	if err = cmp.Or(err, f.Close()); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("copy written", "path", path, "size", len(content), "overwrite", overwrite)
	return nil
}

func newDocUpdateCmd() *cobra.Command {
	var ref docRef
	var file string
	var base int64
	cmd := &cobra.Command{
		Use:   "update (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --path <file> --base <n>",
		Short: "Write a doc's content as a new revision",
		Long: "--base is the revision the change was made on, as doc get printed it. If the\n" +
			"doc has a newer revision, nothing is written: get it again, apply the change\n" +
			"to it and update with the new base, so that no change is lost. A new\n" +
			"revision makes the doc wip, whatever it was; content equal to the latest\n" +
			"makes none and changes nothing.",
		Example: examples(
			"Write back a changed copy", identity.Name+" doc update --name auth-rfc --path auth-rfc.md --base 4",
			"From stdin, as JSON", identity.Name+" doc update --id doc-1a2b3c4d --path - --base 4 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not updated",
			outputNote:    "The doc's revision now.\n" + `With --json: {"result": "updated", "doc": {...}, "previous_status": ...}`,
			resultList:    annotation(string(api.ResultUpdated), string(api.ResultUnchanged)),
			errorList: codeList(api.ErrorCodeDocChanged, api.ErrorCodeInvalidArgument, api.ErrorCodeNotFound,
				api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			content, err := readContent(cmd, file)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).UpdateDocContent(cmd.Context(), path, project, dir, base, content)
			if problemCode(err) == api.ErrorCodeDocChanged {
				return docChangedHint(err, ref)
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			if r.PreviousStatus != nil && *r.PreviousStatus != api.DocStatusWip && !jsonMode(cmd) {
				st := stderrStyle(cmd)
				fmt.Fprintln(cmd.ErrOrStderr(), st.Warning(fmt.Sprintf("%s was %s; it is wip again", r.Doc.Name, *r.PreviousStatus)))
			}
			return printResult(cmd, docUpdated(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&file, "path", "", "`file` holding the new content, - for stdin")
	cmd.Flags().Int64Var(&base, "base", 0, "the `revision` the change was made on")
	_ = cmd.MarkFlagRequired("path")
	_ = cmd.MarkFlagRequired("base")
	completeFlag(cmd, "path", completeFiles)
	completeFlag(cmd, "base", completeDocRevisions)
	return cmd
}

// docChangedHint tells how to apply a change to a doc that moved on since its
// base.
func docChangedHint(err error, ref docRef) error {
	var d api.Doc
	if e, ok := errors.AsType[*client.ProblemError](err); !ok || !decodeDetail(e.Problem, "doc", &d) {
		return err
	}
	return &hinted{err, fmt.Sprintf("get it again with %s doc get %s --out <file> --overwrite, apply your change, and update with --base %d",
		identity.Name, ref.flag(), d.Revision)}
}

func newDocListCmd() *cobra.Command {
	var scope projectScope
	var labels []string
	var status string
	cmd := &cobra.Command{
		Use:   "list [--project-id <id> | --project-name <name>] [--labels <a,b>] [--status <status>]",
		Short: "List a project's docs",
		Long: "Without a project flag, the docs of the project the working directory is in.\n" +
			"With --labels, only those that have every one of them.",
		Example: examples(
			"The docs of the project you are in", identity.Name+" doc list",
			"The approved docs labeled auth, as JSON", identity.Name+" doc list --project-name app --labels auth --status approved --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The docs by folder, then name, without content; an empty list when there are none.\n" + `With --json: {"docs": [...]}`,
			errorList:  codeList(api.ErrorCodeInvalidArgument, api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			project, dir, err := docScope(cmd, scope)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).ListDocs(cmd.Context(), project, dir, labels, api.DocStatus(status))
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, docList(list))
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringSliceVar(&labels, "labels", nil, "`labels` a doc must all have, comma-separated")
	addStatusFlag(cmd, &status, "the `status` a doc must be in")
	completeFlag(cmd, "labels", completeDocLabels)
	return cmd
}

func newDocHistoryCmd() *cobra.Command {
	var ref docRef
	cmd := &cobra.Command{
		Use:   "history (--id <id> | --name <name>) [--project-id <id> | --project-name <name>]",
		Short: "List a doc's revisions",
		Long:  "Every revision is kept until the doc is removed; doc get --revision reads one.",
		Example: examples(
			"The revisions of a doc of the project you are in", identity.Name+" doc history --name auth-rfc",
			"By id, as JSON", identity.Name+" doc history --id doc-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The revisions, latest first, without content.\n" + `With --json: {"revisions": [...]}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).ListDocRevisions(cmd.Context(), path, project, dir)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, docHistory(list))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

func newDocSetCmd() *cobra.Command {
	var ref docRef
	var status, folder string
	var add, remove []string
	cmd := &cobra.Command{
		Use:   "set (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--folder <folder>] [--status <status>] [--add-labels <a,b>] [--remove-labels <a,b>]",
		Short: "Change a doc's folder, status or labels",
		Long: "Its content and revision stay as they are, and no base is needed. Removing a\n" +
			"label it does not have is not an error.",
		Example: examples(
			"Approve a doc of the project you are in", identity.Name+" doc set --name auth-rfc --status approved",
			"Change its labels, as JSON", identity.Name+" doc set --id doc-1a2b3c4d --add-labels auth --remove-labels draft --json",
			"Move it to a folder", identity.Name+" doc set --name auth-rfc --folder /hub/initial",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not changed",
			outputNote:    "The doc's status, folder and labels now.\n" + `With --json: {"result": "updated", "doc": {...}}`,
			resultList:    annotation(string(api.ResultUpdated), string(api.ResultUnchanged)),
			errorList:     codeList(api.ErrorCodeInvalidArgument, api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			body := api.UpdateDoc{Status: optionalStatus(status), AddLabels: optionalSlice(add), RemoveLabels: optionalSlice(remove)}
			if cmd.Flags().Changed("folder") {
				body.Folder = &folder
			}
			r, err := clientOf(cmd).UpdateDoc(cmd.Context(), path, project, dir, body)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, docSet(r))
		},
	}
	ref.addFlags(cmd)
	addStatusFlag(cmd, &status, "`status` to give it: wip, review, approved or rejected")
	cmd.Flags().StringSliceVar(&add, "add-labels", nil, "`labels` to add, comma-separated")
	cmd.Flags().StringSliceVar(&remove, "remove-labels", nil, "`labels` to remove, comma-separated")
	addFolderFlag(cmd, &folder, "`folder` to move it to, such as /hub/initial; / is the top")
	cmd.MarkFlagsOneRequired("folder", "status", "add-labels", "remove-labels")
	completeFlag(cmd, "add-labels", completeDocLabels)
	completeFlag(cmd, "remove-labels", completeDocLabels)
	return cmd
}

func newDocRenameCmd() *cobra.Command {
	var ref docRef
	var newName string
	cmd := &cobra.Command{
		Use:   "rename (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] --new-name <name>",
		Short: "Rename a doc",
		Long:  "Its id, content, revisions and status stay as they are; changing only the case of its name is a rename.",
		Example: examples(
			"Rename a doc of the project you are in", identity.Name+" doc rename --name auth-rfc --new-name auth-design",
			"By id, as JSON", identity.Name+" doc rename --id doc-1a2b3c4d --new-name auth-design --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not renamed",
			outputNote:    "The doc's name before and now.\n" + `With --json: {"result": "updated", "doc": {...}}`,
			resultList:    annotation(string(api.ResultUpdated), string(api.ResultUnchanged)),
			errorList:     codeList(api.ErrorCodeInvalidName, api.ErrorCodeNameTaken, api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			r, err := clientOf(cmd).UpdateDoc(cmd.Context(), path, project, dir, api.UpdateDoc{Name: &newName})
			switch problemCode(err) {
			case api.ErrorCodeNameTaken, api.ErrorCodeInvalidName:
				return &hinted{err, "choose another --new-name"}
			}
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, docRenamed{r, cmp.Or(ref.name, ref.id)})
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().StringVar(&newName, "new-name", "", "the doc's new `name`")
	_ = cmd.MarkFlagRequired("new-name")
	completeFlag(cmd, "new-name", cobra.NoFileCompletions)
	return cmd
}

func newDocSyncToGitCmd() *cobra.Command {
	var scope projectScope
	var path string
	cmd := &cobra.Command{
		Use:   "sync-to-git [--project-id <id> | --project-name <name>] --path <dir>",
		Short: "Write a project's docs, read-only, into a directory of its repository",
		Long: "--path is relative to the project's main checkout, wherever this runs, and is\n" +
			"created when missing. Each doc's latest revision is written as\n" +
			"<name>.<format>; a file of that name that wdx did not write is left as it is.\n" +
			"Files wdx wrote there of docs that are gone are removed, and only those:\n" +
			".wdx-docs lists what it wrote. Nothing is staged or committed. The setting\n" +
			"docs.git_path has the daemon do this on every change.",
		Example: examples(
			"Write the docs of the project you are in", identity.Name+" doc sync-to-git --path docs/wdx",
			"Of another project, as JSON", identity.Name+" doc sync-to-git --project-name app --path docs/wdx --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not synced",
			outputNote: "How many docs were written and where, and the files removed or skipped.\n" +
				`With --json: {"result": "synced", "path": "...", "written": [...], "removed": [...], "skipped": [...]}`,
			resultList: string(api.ResultSynced),
			errorList: codeList(api.ErrorCodeCheckoutMissing, api.ErrorCodeInvalidPath, api.ErrorCodeNotFound,
				api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			project, dir, err := docScope(cmd, scope)
			if err != nil {
				return err
			}
			// Relative to the repository, not to where this runs: never made absolute.
			r, err := clientOf(cmd).SyncDocsToGit(cmd.Context(), project, dir, path)
			if problemCode(err) == api.ErrorCodeInvalidPath {
				return &hinted{err, "give a directory relative to the project's main checkout, outside .git"}
			}
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, docSynced{r, path})
		},
	}
	scope.addFlags(cmd)
	cmd.Flags().StringVar(&path, "path", "", "`dir` to write into, relative to the project's main checkout")
	_ = cmd.MarkFlagRequired("path")
	completeFlag(cmd, "path", cobra.NoFileCompletions)
	return cmd
}

const removeDocQuestion = "remove doc"

func newDocRemoveCmd() *cobra.Command {
	var ref docRef
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove (--id <id> | --name <name>) [--project-id <id> | --project-name <name>] [--yes]",
		Short: "Delete a doc and all its revisions",
		Long: "You are asked first, as nothing brings it back; --yes answers in advance, and\n" +
			"with --json, or without a terminal, it is required.",
		Example: examples(
			"Remove a doc of the project you are in", identity.Name+" doc remove --name auth-rfc",
			"From a script", identity.Name+" doc remove --id doc-1a2b3c4d --yes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not removed",
			outputNote:    "The removed doc, as it was before.\n" + `With --json: {"result": "removed", "doc": {...}}`,
			resultList:    string(api.ResultRemoved),
			errorList:     codeList(api.ErrorCodeCancelled, api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
			jsonRequires:  annotation("yes"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, project, dir, err := ref.resolve(cmd)
			if err != nil {
				return err
			}
			if yes {
				logAnswer(cmd, removeDocQuestion, "yes", "--yes")
			} else if err := confirmDocRemove(cmd, ref, path, project, dir); err != nil {
				return err
			}
			r, err := clientOf(cmd).DeleteDoc(cmd.Context(), path, project, dir)
			if err != nil {
				return ref.notFoundHint(err)
			}
			return printResult(cmd, docResult(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().BoolVar(&yes, "yes", false, "remove it without asking")
	return cmd
}

// confirmDocRemove asks whether to remove the doc; nil means yes.
func confirmDocRemove(cmd *cobra.Command, ref docRef, path, project, dir string) error {
	if !canPrompt(cmd) {
		logUnanswered(cmd, removeDocQuestion)
		return &usageError{fmt.Errorf("removing a doc needs an answer, and none can be asked (%s): pass --yes", noPromptReason(cmd))}
	}
	d, err := clientOf(cmd).GetDoc(cmd.Context(), path, project, dir, 0)
	if err != nil {
		return ref.notFoundHint(err)
	}
	prompt, st := cmd.ErrOrStderr(), stderrStyle(cmd)
	question := fmt.Sprintf("remove doc %s and its %s? %s: ", st.Name(d.Doc.Name), plural(int(d.Doc.Revision), "revision"), st.Command("[y/N]"))
	answer, err := ask(prompt, bufio.NewScanner(cmd.InOrStdin()), question, "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		logAnswer(cmd, removeDocQuestion, "no", "prompt")
		return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you answered no")}
	}
	logAnswer(cmd, removeDocQuestion, "yes", "prompt")
	return nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func optionalSlice(s []string) *[]string {
	if len(s) == 0 {
		return nil
	}
	return &s
}

func optionalStatus(s string) *api.DocStatus {
	if s == "" {
		return nil
	}
	status := api.DocStatus(s)
	return &status
}

// completeDocs completes --id or --name, as key says, with the docs of the
// project the command line names, else of the one the working directory is in.
func completeDocs(key string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var completions []cobra.Completion
		for _, d := range completionDocs(cmd) {
			if key == "id" {
				completions = append(completions, cobra.CompletionWithDesc(d.Id, fmt.Sprintf("%s in %s", d.Name, d.ProjectName)))
			} else {
				completions = append(completions, cobra.CompletionWithDesc(d.Name, fmt.Sprintf("%s, %s", d.Status, d.Id)))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeDocLabels completes a label with those the project's docs have.
func completeDocLabels(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	seen := map[string]bool{}
	var completions []cobra.Completion
	for _, d := range completionDocs(cmd) {
		for _, l := range d.Labels {
			if !seen[l] {
				seen[l] = true
				completions = append(completions, l)
			}
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// completionDocs are the docs of the project the command line names, else of the
// one the working directory is in; none when the daemon does not answer.
func completionDocs(cmd *cobra.Command) []api.Doc {
	ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(invocationOf(cmd).env.Timeouts.Completion, completionTimeout))
	defer cancel()
	var scope projectScope
	scope.id, _ = cmd.Flags().GetString("project-id")
	scope.name, _ = cmd.Flags().GetString("project-name")
	var dir string
	if !scope.given() {
		dir = invocationOf(cmd).env.WorkDir
	}
	list, err := clientOf(cmd).ListDocs(ctx, scope.ref(), dir, nil, "")
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no docs to complete", "err", err)
		return nil
	}
	return list.Docs
}

// completeDocRevisions completes --revision or --base with the revisions of the
// doc the command line names.
func completeDocRevisions(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(invocationOf(cmd).env.Timeouts.Completion, completionTimeout))
	defer cancel()
	var ref docRef
	ref.id, _ = cmd.Flags().GetString("id")
	ref.name, _ = cmd.Flags().GetString("name")
	ref.scope.id, _ = cmd.Flags().GetString("project-id")
	ref.scope.name, _ = cmd.Flags().GetString("project-name")
	if ref.id == "" && ref.name == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	path, project, dir, err := ref.resolve(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	list, err := clientOf(cmd).ListDocRevisions(ctx, path, project, dir)
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no revisions to complete", "err", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []cobra.Completion
	for _, r := range list.Revisions {
		completions = append(completions, cobra.CompletionWithDesc(strconv.FormatInt(r.Number, 10), r.CreatedAt.Local().Format(time.DateTime)))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// docResult is a doc a command created or removed.
type docResult api.DocResult

func (r docResult) text(w io.Writer, st style.Style) {
	d := r.Doc
	fmt.Fprintf(w, "doc %s %s\n", st.Name(d.Name), st.Success(string(r.Result)))
	docFields(w, st, d)
}

func docFields(w io.Writer, st style.Style, d api.Doc) {
	field := fieldPrinter(w, st, 8)
	field("project", d.ProjectName)
	field("folder", d.Folder)
	field("status", docStatusText(st, d.Status))
	field("revision", strconv.FormatInt(d.Revision, 10))
	if len(d.Labels) > 0 {
		field("labels", strings.Join(d.Labels, ", "))
	}
	if d.Format != "" {
		field("format", d.Format)
	}
	field("id", st.Muted(d.Id))
}

// docUpdated is a doc whose content a command wrote.
type docUpdated api.DocResult

func (r docUpdated) text(w io.Writer, st style.Style) {
	if r.Result == api.ResultUnchanged {
		fmt.Fprintf(w, "%s is unchanged at revision %d\n", st.Name(r.Doc.Name), r.Doc.Revision)
		return
	}
	fmt.Fprintf(w, "%s is at revision %s\n", st.Name(r.Doc.Name), st.Success(strconv.FormatInt(r.Doc.Revision, 10)))
}

// docSet is a doc whose status or labels a command set.
type docSet api.DocResult

func (r docSet) text(w io.Writer, st style.Style) {
	d := r.Doc
	already := ""
	if r.Result == api.ResultUnchanged {
		already = "already "
	}
	labels := "no labels"
	if len(d.Labels) > 0 {
		labels = "labels " + strings.Join(d.Labels, ", ")
	}
	fmt.Fprintf(w, "doc %s is %s%s, in %s, with %s\n", st.Name(d.Name), already, docStatusText(st, d.Status), st.Name(d.Folder), labels)
}

// docRenamed is a doc a command renamed from the name or id it was given by.
type docRenamed struct {
	api.DocResult
	from string
}

func (r docRenamed) text(w io.Writer, st style.Style) {
	if r.Result == api.ResultUnchanged {
		fmt.Fprintf(w, "doc %s already has that name\n", st.Name(r.Doc.Name))
		return
	}
	fmt.Fprintf(w, "doc %s renamed to %s\n", st.Name(r.from), st.Name(r.Doc.Name))
}

// docContent is a revision of a doc: its content, or where it was written.
type docContent struct {
	Doc      api.Doc `json:"doc"`
	Revision int64   `json:"revision"`
	Content  *string `json:"content,omitempty"`
	out      string
}

func (r docContent) text(w io.Writer, st style.Style) {
	if r.Content != nil {
		fmt.Fprint(w, *r.Content)
		return
	}
	fmt.Fprintf(w, "revision %d of %s written to %s\n", r.Revision, st.Name(r.Doc.Name), r.out)
}

// LogValue measures the content: a doc can hold secrets.
func (r docContent) LogValue() slog.Value {
	attrs := []slog.Attr{slog.Any("doc", r.Doc), slog.Int64("revision", r.Revision)}
	if r.Content != nil {
		attrs = append(attrs, slog.Int("content_bytes", len(*r.Content)))
	}
	return slog.GroupValue(attrs...)
}

// docSynced is what a sync to git did, in the directory given as dir.
type docSynced struct {
	api.DocSyncResult
	dir string
}

func (r docSynced) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s written to %s\n", plural(len(r.Written), "doc"), st.Name(r.dir))
	if len(r.Removed) > 0 {
		fmt.Fprintf(w, "removed %s\n", strings.Join(r.Removed, ", "))
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "%s %s, as a file wdx did not write has the name\n", st.Warning("skipped"), strings.Join(r.Skipped, ", "))
	}
}

// docList is a project's docs.
type docList api.DocList

func (l docList) text(w io.Writer, st style.Style) {
	if len(l.Docs) == 0 {
		fmt.Fprintln(w, "no docs")
		return
	}
	var table style.Table
	table.Row(st.Heading("FOLDER"), st.Heading("NAME"), st.Heading("STATUS"), st.Heading("REVISION"), st.Heading("LABELS"), st.Heading("UPDATED"))
	for _, d := range l.Docs {
		table.Row(d.Folder, st.Name(d.Name), docStatusText(st, d.Status), strconv.FormatInt(d.Revision, 10), st.Muted(strings.Join(d.Labels, ", ")),
			d.UpdatedAt.Local().Format(time.DateTime))
	}
	table.Write(w)
}

// docHistory is a doc's revisions.
type docHistory api.DocRevisionList

func (h docHistory) text(w io.Writer, st style.Style) {
	var table style.Table
	table.Row(st.Heading("REVISION"), st.Heading("SIZE"), st.Heading("CREATED"))
	for _, r := range h.Revisions {
		table.Row(strconv.FormatInt(r.Number, 10), strconv.FormatInt(r.Size, 10), r.CreatedAt.Local().Format(time.DateTime))
	}
	table.Write(w)
}

func docStatusText(st style.Style, s api.DocStatus) string {
	switch s {
	case api.DocStatusApproved:
		return st.Success(string(s))
	case api.DocStatusRejected:
		return st.Failure(string(s))
	}
	return st.Muted(string(s))
}
