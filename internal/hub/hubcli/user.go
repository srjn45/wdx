package hubcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/style"
)

// completionTimeout is the longest a completion waits for the hub; it offers
// nothing when the hub does not answer by then.
const completionTimeout = time.Second

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Invite people and manage their accounts",
		Long: "A person signs up from an invite's link with a username, a password and an\n" +
			"authenticator app; a reset's link sets a new password. Both links are secrets:\n" +
			"hand them only to the person they are for.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newUserInviteCmd(), newUserListCmd(), newUserGetCmd(),
		newUserDisableCmd(true), newUserDisableCmd(false), newUserResetCmd(), newUserRemoveCmd())
	return cmd
}

func newUserInviteCmd() *cobra.Command {
	var expires time.Duration
	cmd := &cobra.Command{
		Use:   "invite [--expires <duration>]",
		Short: "Make a link that lets one person sign up",
		Long: "The link works once, for 24 hours unless --expires says otherwise, at most\n" +
			"30 days (720h). Whoever has it can make an account: hand it only to the\n" +
			"person invited. It is printed, never logged.",
		Example: clikit.Examples(
			"Invite a person", name+" user invite",
			"Invite a person who signs up within three days", name+" user invite --expires 72h",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not invited",
			clikit.OutputNote: "When the invite expires, then its link on a line of its own.\n" +
				`With --json: {"result": "invited", "invite": {"link": ..., "expires_at": ...}}`,
			clikit.Results: clikit.Annotation(string(hubapi.ResultInvited)),
			clikit.Errors:  clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodeInvalidArgument),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var seconds *int64
			if cmd.Flags().Changed("expires") {
				if expires <= 0 {
					return &clikit.UsageError{Err: fmt.Errorf("--expires %s is not more than 0", expires)}
				}
				if expires%time.Second != 0 {
					return &clikit.UsageError{Err: fmt.Errorf("--expires %s is not a whole number of seconds", expires)}
				}
				s := int64(expires / time.Second)
				seconds = &s
			}
			r, err := clientOf(cmd).createInvite(cmd.Context(), seconds)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, inviteResult(r))
		},
	}
	cmd.Flags().DurationVar(&expires, "expires", 0, "`duration` the link works for, such as 72h; 24h when not given")
	clikit.CompleteFlag(cmd, "expires", cobra.NoFileCompletions)
	return cmd
}

type inviteResult hubapi.InviteResult

func (r inviteResult) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s: the link works once, until %s\n", st.Success("invited"), st.Name(timeText(r.Invite.ExpiresAt)))
	fmt.Fprintln(w, r.Invite.Link)
}

// LogValue measures the link: it is a secret.
func (r inviteResult) LogValue() slog.Value { return linkLogged(r) }

func newUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the users, by username",
		Long:  "Removed users are left out.",
		Example: clikit.Examples(
			"List the users", name+" user list",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "A row per user: username, second factor, whether disabled, nodes, created and last login.\n" +
				`With --json: {"users": [{"username": ..., "created_at": ..., "last_login_at": ..., "disabled": ..., "second_factor": ..., "nodes": ...}, ...]}`,
			clikit.Errors: clikit.CodeList(hubapi.ErrorCodeHubUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).users(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, userList(r))
		},
	}
}

type userList hubapi.UserList

func (l userList) Text(w io.Writer, st style.Style) {
	if len(l.Users) == 0 {
		fmt.Fprintf(w, "no users: invite one with %s\n", st.Command(name+" user invite"))
		return
	}
	var t style.Table
	t.Row(st.Muted("USERNAME"), st.Muted("FACTOR"), st.Muted("STATE"), st.Muted("NODES"), st.Muted("CREATED"), st.Muted("LAST LOGIN"))
	for _, u := range l.Users {
		t.Row(st.Name(u.Username), factorText(u.SecondFactor), stateText(st, u.Disabled), strconv.Itoa(u.Nodes), timeText(u.CreatedAt), lastLoginText(u.LastLoginAt))
	}
	t.Write(w)
}

func newUserGetCmd() *cobra.Command {
	var username string
	cmd := &cobra.Command{
		Use:   "get --username <name>",
		Short: "Show one user",
		Example: clikit.Examples(
			"Show a user", name+" user get --username alice",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The user, with their browser sessions counted, and their nodes.\n" +
				`With --json: {"user": {"username": ..., "created_at": ..., "last_login_at": ..., "disabled": ..., "second_factor": ..., "sessions": ..., ` +
				`"nodes": ..., "node_list": [{"id": ..., "name": ..., "os": ..., "version": ..., "created_at": ..., "last_seen_at": ..., "online": ...}, ...]}}`,
			clikit.Errors: clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodeNotFound),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).user(cmd.Context(), username)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, userDetail(r))
		},
	}
	usernameFlag(cmd, &username)
	return cmd
}

type userDetail hubapi.UserEnvelope

func (d userDetail) Text(w io.Writer, st style.Style) {
	u := d.User
	fmt.Fprintf(w, "user %s\n", st.Name(u.Username))
	field := clikit.FieldPrinter(w, st, 10)
	field("factor", factorText(u.SecondFactor))
	field("state", stateText(st, u.Disabled))
	field("created", timeText(u.CreatedAt))
	field("last login", lastLoginText(u.LastLoginAt))
	field("sessions", strconv.Itoa(u.Sessions))
	field("nodes", strconv.Itoa(u.Nodes))
	if len(u.NodeList) == 0 {
		return
	}
	var t style.Table
	t.Row(st.Muted("NODE"), st.Muted("ID"), st.Muted("STATE"), st.Muted("OS"), st.Muted("WDX"), st.Muted("LAST SEEN"))
	for _, n := range u.NodeList {
		state := st.Muted("offline")
		if n.Online {
			state = st.Success("online")
		}
		seen := "never"
		if n.LastSeenAt != nil {
			seen = timeText(*n.LastSeenAt)
		}
		t.Row(st.Name(n.Name), n.Id, state, n.Os, n.Version, seen)
	}
	fmt.Fprintln(w)
	t.Write(w)
}

// newUserDisableCmd is user disable, or user enable when disable is false.
func newUserDisableCmd(disable bool) *cobra.Command {
	var username string
	verb, short, long, failure := "enable", "Let a disabled user log in again", "", "not enabled"
	if disable {
		verb, short, failure = "disable", "Keep a user from logging in", "not disabled"
		long = "Their browser sessions end and their nodes are cut off, and their logins and\nnodes are refused until " + name + " user enable."
	}
	cmd := &cobra.Command{
		Use:   verb + " --username <name>",
		Short: short,
		Long:  long,
		Example: clikit.Examples(
			short, name+" user "+verb+" --username alice",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: failure,
			clikit.OutputNote: "The user, " + verb + "d, or unchanged when they were so already.\n" +
				`With --json: {"result": ..., "user": {...}}`,
			clikit.Results: clikit.Annotation(verb+"d", string(hubapi.ResultUnchanged)),
			clikit.Errors:  clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodeNotFound),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).updateUser(cmd.Context(), username, disable)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, userResult(r))
		},
	}
	usernameFlag(cmd, &username)
	return cmd
}

type userResult hubapi.UserResult

func (r userResult) Text(w io.Writer, st style.Style) {
	u := st.Name(r.User.Username)
	switch r.Result {
	case hubapi.ResultUnchanged:
		fmt.Fprintf(w, "user %s is %s already\n", u, stateText(st, r.User.Disabled))
	case hubapi.ResultRemoved:
		fmt.Fprintf(w, "%s user %s; the username stays taken\n", st.Success("removed"), u)
	case hubapi.ResultDisabled:
		fmt.Fprintf(w, "%s user %s; their browser sessions ended and their nodes were cut off\n", st.Success("disabled"), u)
	default:
		fmt.Fprintf(w, "%s user %s\n", st.Success(string(r.Result)), u)
	}
}

func newUserResetCmd() *cobra.Command {
	var username string
	var secondFactor bool
	cmd := &cobra.Command{
		Use:   "reset --username <name> [--second-factor]",
		Short: "Make a link that sets a new password for a user",
		Long: "The link works once, for 24 hours, and voids an earlier one; using it ends the\n" +
			"user's browser sessions. With --second-factor it also removes their\n" +
			"authenticator and recovery codes, so that they enrol a new one when they next\n" +
			"log in. Whoever has the link can take the account over: hand it only to the\n" +
			"user. It is printed, never logged.",
		Example: clikit.Examples(
			"Let a user who forgot their password set a new one", name+" user reset --username alice",
			"Let a user who lost their authenticator too in again", name+" user reset --username alice --second-factor",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not reset",
			clikit.OutputNote: "When the reset expires, then its link on a line of its own.\n" +
				`With --json: {"result": "reset", "reset": {"link": ..., "expires_at": ..., "second_factor": ...}}`,
			clikit.Results: clikit.Annotation(string(hubapi.ResultReset)),
			clikit.Errors:  clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodeNotFound),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).createReset(cmd.Context(), username, secondFactor)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, resetResult{ResetResult: r, username: username})
		},
	}
	usernameFlag(cmd, &username)
	cmd.Flags().BoolVar(&secondFactor, "second-factor", false, "also remove their authenticator and recovery codes")
	return cmd
}

type resetResult struct {
	hubapi.ResetResult
	username string
}

func (r resetResult) Text(w io.Writer, st style.Style) {
	what := "password"
	if r.Reset.SecondFactor {
		what = "password and second factor"
	}
	fmt.Fprintf(w, "%s the %s of %s: the link works once, until %s\n", st.Success("reset"), what, st.Name(r.username), st.Name(timeText(r.Reset.ExpiresAt)))
	fmt.Fprintln(w, r.Reset.Link)
}

// LogValue measures the link: it is a secret.
func (r resetResult) LogValue() slog.Value { return linkLogged(r.ResetResult) }

const removeUserQuestion = "remove user"

func newUserRemoveCmd() *cobra.Command {
	var username string
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove --username <name> [--yes]",
		Short: "Remove a user, keeping their username taken",
		Long: "Their password, authenticator, recovery codes, browser sessions, resets,\n" +
			"nodes and docs are deleted, their nodes cut off, and the username is never\n" +
			"given again.\n" +
			"You are asked first, as nothing brings it back; --yes answers in advance, and\n" +
			"with --json, or without a terminal, it is required.",
		Example: clikit.Examples(
			"Remove a user", name+" user remove --username alice",
			"From a script", name+" user remove --username alice --yes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not removed",
			clikit.OutputNote:    "The removed user, as they were.\n" + `With --json: {"result": "removed", "user": {...}}`,
			clikit.Results:       clikit.Annotation(string(hubapi.ResultRemoved)),
			clikit.Errors:        clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodeNotFound, hubapi.ErrorCodeCancelled),
			clikit.JSONRequires:  clikit.Annotation("yes"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if yes {
				clikit.LogAnswer(cmd, removeUserQuestion, "yes", "--yes")
			} else if err := confirmUserRemove(cmd, username); err != nil {
				return err
			}
			r, err := clientOf(cmd).removeUser(cmd.Context(), username)
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, userResult(r))
		},
	}
	usernameFlag(cmd, &username)
	cmd.Flags().BoolVar(&yes, "yes", false, "remove them without asking")
	return cmd
}

// confirmUserRemove asks whether to remove the user; nil means yes.
func confirmUserRemove(cmd *cobra.Command, username string) error {
	if !clikit.CanPrompt(cmd) {
		clikit.LogUnanswered(cmd, removeUserQuestion)
		return &clikit.UsageError{Err: fmt.Errorf("removing a user needs an answer, and none can be asked (%s): pass --yes", clikit.NoPromptReason(cmd))}
	}
	d, err := clientOf(cmd).user(cmd.Context(), username)
	if err != nil {
		return err
	}
	prompt, st := cmd.ErrOrStderr(), clikit.StderrStyle(cmd)
	question := fmt.Sprintf("remove user %s? Their username stays taken. %s: ", st.Name(d.User.Username), st.Command("[y/N]"))
	answer, err := clikit.Ask(prompt, bufio.NewScanner(cmd.InOrStdin()), question, "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		clikit.LogAnswer(cmd, removeUserQuestion, "no", "prompt")
		return &problemError{problem: hubapi.NewProblem(hubapi.ErrorCodeCancelled, "you answered no")}
	}
	clikit.LogAnswer(cmd, removeUserQuestion, "yes", "prompt")
	return nil
}

// usernameFlag adds the required --username, completed with the users.
func usernameFlag(cmd *cobra.Command, username *string) {
	cmd.Flags().StringVar(username, "username", "", "the user's `name`, whatever its case")
	cmd.MarkFlagRequired("username")
	clikit.CompleteFlag(cmd, "username", completeUsers)
}

// completeUsers offers the usernames, or nothing when the hub does not
// answer within completionTimeout.
func completeUsers(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	l, err := clientOf(cmd).users(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []cobra.Completion
	for _, u := range l.Users {
		completions = append(completions, cobra.CompletionWithDesc(u.Username, factorText(u.SecondFactor)+", "+stateText(style.Style{}, u.Disabled)))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// linkLogged is r, holding an invite or a reset, as logged: its link
// measured.
func linkLogged(r any) slog.Value {
	b, err := json.Marshal(r)
	if err != nil {
		return slog.StringValue("unreadable response")
	}
	return slog.AnyValue(hubapi.LinkResponseLogged(b))
}

func timeText(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func lastLoginText(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return timeText(*t)
}

func factorText(factor *string) string {
	if factor == nil {
		return "none yet"
	}
	return *factor
}

func stateText(st style.Style, disabled bool) string {
	if disabled {
		return st.Warning("disabled")
	}
	return "enabled"
}
