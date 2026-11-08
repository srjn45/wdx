package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newProjectSyncCmd() *cobra.Command {
	var ref projectRef
	var on, off bool
	cmd := &cobra.Command{
		Use:   "sync (--id <id> | --name <name>) [--on | --off]",
		Short: "Show, start or stop pushing a project's docs to the hub",
		Long: "With --on, every doc of the project goes up to the hub with all its revisions,\n" +
			"then each change as it happens; plans never do. With --off, it stops, and\n" +
			"what is on the hub stays there. Both work logged out or offline: changes\n" +
			"wait for the connection. Without either, it shows what is waiting and what\n" +
			"the hub refused. The project needs a git remote, whose key the hub files\n" +
			"its docs under.",
		Example: clikit.Examples(
			"Show how far a project's docs are on the hub", identity.Name+" project sync --name app",
			"Push its docs", identity.Name+" project sync --name app --on",
			"Stop, as JSON", identity.Name+" project sync --id proj-1a2b3c4d --off --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "Whether it is on, the changes waiting, each doc the hub refused with its code, when a\n" +
				"change last reached the hub and the repository key.\n" +
				`With --json: {"sync": {"on": true, "pending": 3, "failed": [...], "last_pushed_at": "...", "repo_key": "...", "logged_in": true}},` + "\n" +
				`with "result" too given --on or --off`,
			clikit.Results: clikit.Annotation("changed", "unchanged"),
			clikit.Errors:  clikit.CodeList(api.ErrorCodeNoRemote, api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !on && !off {
				s, err := clientOf(cmd).ProjectSync(cmd.Context(), ref.path())
				if err != nil {
					return syncHint(err)
				}
				return clikit.PrintResult(cmd, projectSync{Sync: s.Sync})
			}
			r, err := clientOf(cmd).SetProjectSync(cmd.Context(), ref.path(), on)
			if err != nil {
				return syncHint(err)
			}
			return clikit.PrintResult(cmd, projectSyncResult(r))
		},
	}
	ref.addFlags(cmd, "")
	cmd.Flags().BoolVar(&on, "on", false, "push the project's docs to the hub")
	cmd.Flags().BoolVar(&off, "off", false, "stop pushing them")
	cmd.MarkFlagsMutuallyExclusive("on", "off")
	return cmd
}

func syncHint(err error) error {
	if problemCode(err) == api.ErrorCodeNoRemote {
		return &clikit.Hinted{Err: err, Hint: "add a git remote to the project's repository: git remote add origin <url>"}
	}
	return projectNotFoundHint(err)
}

type projectSync api.ProjectSyncEnvelope

func (s projectSync) Text(w io.Writer, st style.Style) { syncText(w, st, s.Sync) }

type projectSyncResult api.ProjectSyncResult

func (r projectSyncResult) Text(w io.Writer, st style.Style) {
	state := "off"
	if r.Sync.On {
		state = "on"
	}
	if r.Result == api.ResultChanged {
		fmt.Fprintf(w, "sync %s\n", st.Success("turned "+state))
	} else {
		fmt.Fprintf(w, "sync %s already\n", st.Warning(state))
	}
	syncText(w, st, r.Sync)
}

// syncText writes the sync s: its state, what is waiting and when it last
// reached the hub, then each doc the hub refused.
func syncText(w io.Writer, st style.Style, s api.ProjectSync) {
	if !s.On {
		fmt.Fprintf(w, "sync off, %s\n", repoText(st, s.RepoKey))
	} else {
		pushed := "never pushed"
		if s.LastPushedAt != nil {
			pushed = "last pushed " + ago(*s.LastPushedAt)
		}
		fmt.Fprintf(w, "sync on, %s, %s, %s\n", waitingText(s.Pending), pushed, repoText(st, s.RepoKey))
	}
	for _, f := range s.Failed {
		name := f.Name
		if name == "" {
			name = f.DocId
		}
		fmt.Fprintf(w, "  %s %s refused by the hub  [%s]\n", st.Failure("failed:"), st.Name(name), f.Code)
		if f.Code == "doc_name_taken" && f.Name != "" {
			writeHint(w, st, "another of your docs in this repository has its name on the hub; rename it: "+identity.Name+" doc rename --id "+f.DocId+" --new-name <name>")
		}
	}
	if s.On && !s.LoggedIn {
		writeHint(w, st, "not logged in: changes wait until "+identity.Name+" hub login")
	}
}

func waitingText(n int) string {
	if n == 1 {
		return "1 change waiting"
	}
	return fmt.Sprintf("%d changes waiting", n)
}

func repoText(st style.Style, key *string) string {
	if key == nil {
		return st.Muted("no git remote")
	}
	return "repository " + st.Name(*key)
}

// ago is how long ago t was, roughly.
func ago(t time.Time) string {
	d := time.Since(t)
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute")
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour")
	default:
		return plural(int(d/(24*time.Hour)), "day")
	}
}
