package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

func newHubSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Open, list and close sessions between this node and another of your account",
		Long: "A session into another node is opened with that node's authenticator code,\n" +
			"and is end-to-end encrypted between the two nodes' own keys: the hub passes\n" +
			"its bytes and cannot read them. It ends when closed, when it goes unused for\n" +
			"the host's hub.session_idle_timeout, when the host's daemon restarts, or when\n" +
			"remote access goes off there.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHubSessionCloseCmd(), newHubSessionListCmd(), newHubSessionOpenCmd())
	return cmd
}

func newHubSessionOpenCmd() *cobra.Command {
	var node, code string
	cmd := &cobra.Command{
		Use:   "open --node <name> [--code <code>]",
		Short: "Open a session into another node of your account, with its code",
		Long: "The code is the one your authenticator app shows for that node (wdx hub code\n" +
			"setup there); without --code it is asked for, and --json needs it. A code\n" +
			"opens one session, and is never logged. After a wrong code, the next try\n" +
			"waits, from 1 second doubling to 5 minutes. With a session into the node\n" +
			"open already, nothing is asked.",
		Example: clikit.Examples(
			"Open a session into the desktop, typing its code", identity.Name+" hub session open --node desktop",
			"From a script", identity.Name+" hub session open --node desktop --code 492817 --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "no session opened",
			clikit.OutputNote:    "The session.\n" + `With --json: {"result": "opened", "session": {...}}`,
			clikit.Results:       clikit.Annotation(string(api.ResultOpened), string(api.ResultUnchanged)),
			clikit.Errors: clikit.CodeList(api.ErrorCodeCodeNotSetUp, api.ErrorCodeCodeWrong, api.ErrorCodeNodeKeyMismatch, api.ErrorCodeNodeNotFound,
				api.ErrorCodeNodeOffline, api.ErrorCodeNodeVersionUnsupported, api.ErrorCodeRemoteAccessOff, api.ErrorCodeSessionClosed, api.ErrorCodeTooManyStreams,
				api.ErrorCodeHubUnreachable, api.ErrorCodeHubVersionUnsupported, api.ErrorCodeNotLoggedIn, api.ErrorCodeDaemonUnavailable),
			clikit.JSONRequires: clikit.CodeFlag,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := clientOf(cmd)
			if err := notThisNode(cmd.Context(), c, node, "a session opens into another node of your account"); err != nil {
				return err
			}
			if !cmd.Flags().Changed(clikit.CodeFlag) {
				if open, ok, err := openSessionInto(cmd, node); err != nil || ok {
					if err != nil {
						return err
					}
					return clikit.PrintResult(cmd, hubSessionResult{Result: api.ResultUnchanged, Session: open})
				}
				var err error
				if code, err = askCode(cmd, node); err != nil {
					return err
				}
			}
			r, err := c.OpenHubSession(cmd.Context(), node, strings.TrimSpace(code))
			if err != nil {
				return sessionHint(err, node)
			}
			return clikit.PrintResult(cmd, hubSessionResult(r))
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "`name` of the node to open a session into")
	cmd.Flags().StringVar(&code, clikit.CodeFlag, "", "the node's authenticator `code` now; asked for when not given")
	cmd.MarkFlagRequired("node")
	clikit.CompleteFlag(cmd, "node", completeHubNodes("name"))
	clikit.CompleteFlag(cmd, clikit.CodeFlag, cobra.NoFileCompletions)
	return cmd
}

// openSessionInto is the session open from this node into the node named
// name, false when there is none: then a code is needed. This node's row is
// only its guess, so a stream checks it with the host, which closes it here
// too when the host closed it.
func openSessionInto(cmd *cobra.Command, name string) (api.HubSession, bool, error) {
	c := clientOf(cmd)
	list, err := c.HubSessions(cmd.Context())
	if err != nil {
		return api.HubSession{}, false, err
	}
	for _, s := range list.Sessions {
		if s.Direction != api.HubSessionDirectionOutgoing || !strings.EqualFold(s.Node.Name, name) && "id:"+s.Node.Id != name {
			continue
		}
		conn, err := c.Connect(cmd.Context(), name)
		if problemCode(err) == api.ErrorCodeSessionRequired {
			logging.FromContext(cmd.Context()).Info("session closed at the host", "session_id", s.Id)
			return api.HubSession{}, false, nil
		}
		if err != nil {
			return api.HubSession{}, false, err
		}
		conn.Close()
		return s, true, nil
	}
	return api.HubSession{}, false, nil
}

// askCode asks for the node's code on the terminal; it is never logged.
func askCode(cmd *cobra.Command, node string) (string, error) {
	if !clikit.CanPrompt(cmd) {
		clikit.LogUnanswered(cmd, "code for "+node)
		return "", &clikit.UsageError{Err: fmt.Errorf("opening a session needs the node's code, and none can be asked (%s): pass --code", clikit.NoPromptReason(cmd))}
	}
	code, err := clikit.Ask(cmd.ErrOrStderr(), bufio.NewScanner(cmd.InOrStdin()), "code for "+clikit.StderrStyle(cmd).Name(node)+": ")
	if err != nil {
		return "", err
	}
	logging.FromContext(cmd.Context()).Info("code typed", "code_bytes", len(code))
	return code, nil
}

// sessionHint adds to a session's failure what to do about it.
func sessionHint(err error, node string) error {
	hint := ""
	switch problemCode(err) {
	case api.ErrorCodeCodeWrong:
		hint = "type the code the app shows now for " + node + ", once the wait is over"
	case api.ErrorCodeCodeNotSetUp:
		hint = "run " + identity.Name + " hub code setup on " + node
	case api.ErrorCodeRemoteAccessOff:
		hint = "run " + identity.Name + " setting set --name hub.remote_access --value true on " + node
	case api.ErrorCodeNodeOffline:
		hint = "check that the daemon runs on " + node + " and is connected: " + identity.Name + " hub status there"
	case api.ErrorCodeNodeKeyMismatch:
		hint = "compare " + identity.Name + " hub node get --name " + node + " here and on " + node + "; until the fingerprints match, do not trust the hub"
	case api.ErrorCodeNodeVersionUnsupported:
		hint = "update wdx on the older node: " + identity.Name + " update"
	case api.ErrorCodeTooManyStreams:
		hint = "close sessions you do not use: " + identity.Name + " hub session list"
	default:
		return notLoggedInHint(err)
	}
	return &clikit.Hinted{Err: err, Hint: hint}
}

type hubSessionResult api.HubSessionResult

func (r hubSessionResult) Text(w io.Writer, st style.Style) {
	s := r.Session
	if r.Result == api.ResultUnchanged {
		fmt.Fprintf(w, "%s: a session into node %s is open already, %s\n", st.Success("unchanged"), st.Name(s.Node.Name), st.Muted(s.Id))
		return
	}
	fmt.Fprintf(w, "%s a session into node %s, %s; it closes after %s unused\n", st.Success("opened"), st.Name(s.Node.Name), st.Muted(s.Id),
		s.IdleExpiresAt.Sub(s.LastUsedAt).Round(time.Second))
}

func newHubSessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the sessions open into this node and from it",
		Long:  "It asks no hub: the sessions are this node's own.",
		Example: clikit.Examples(
			"Every session open", identity.Name+" hub session list",
			"As JSON", identity.Name+" hub session list --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The sessions, by when they opened.\n" + `With --json: {"sessions": [...]}`,
			clikit.Errors:     clikit.CodeList(api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).HubSessions(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, hubSessionList(r))
		},
	}
}

type hubSessionList api.HubSessionList

func (l hubSessionList) Text(w io.Writer, st style.Style) {
	if len(l.Sessions) == 0 {
		fmt.Fprintln(w, "no sessions open")
		return
	}
	var t style.Table
	t.Row(st.Heading("ID"), st.Heading("DIRECTION"), st.Heading("NODE"), st.Heading("OPENED"), st.Heading("LAST USED"), st.Heading("IDLE UNTIL"))
	for _, s := range l.Sessions {
		t.Row(st.Muted(s.Id), string(s.Direction), st.Name(s.Node.Name), s.OpenedAt.Local().Format(time.DateTime),
			s.LastUsedAt.Local().Format(time.DateTime), s.IdleExpiresAt.Local().Format(time.DateTime))
	}
	t.Write(w)
}

func newHubSessionCloseCmd() *cobra.Command {
	var id string
	var all bool
	cmd := &cobra.Command{
		Use:   "close (--id <id> | --all)",
		Short: "Close a session, or every one, either way",
		Long: "Closing an outgoing session ends this node's access, telling the host when it\n" +
			"can; closing an incoming one cuts off whoever is connected from that node,\n" +
			"which finds out at its next use.",
		Example: clikit.Examples(
			"Close one session", identity.Name+" hub session close --id sess-1a2b3c4d5e6f7a8b",
			"Close every one, from a script", identity.Name+" hub session close --all --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not closed",
			clikit.OutputNote: "The sessions closed, as they were.\n" +
				`With --json: {"result": "closed", "session": {...}}, or "sessions": [...] with --all`,
			clikit.Results: string(api.ResultClosed),
			clikit.Errors:  clikit.CodeList(api.ErrorCodeSessionNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := clientOf(cmd)
			if all {
				r, err := c.CloseHubSessions(cmd.Context())
				if err != nil {
					return err
				}
				return clikit.PrintResult(cmd, hubSessionsClosed(r))
			}
			r, err := c.CloseHubSession(cmd.Context(), id)
			if err != nil {
				if problemCode(err) == api.ErrorCodeSessionNotFound {
					return &clikit.Hinted{Err: err, Hint: "run " + identity.Name + " hub session list to see the sessions open"}
				}
				return err
			}
			return clikit.PrintResult(cmd, hubSessionClosed(r))
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "`id` of the session")
	cmd.Flags().BoolVar(&all, "all", false, "close every session open, both ways")
	cmd.MarkFlagsOneRequired("id", "all")
	cmd.MarkFlagsMutuallyExclusive("id", "all")
	clikit.CompleteFlag(cmd, "id", completeHubSessions)
	return cmd
}

type hubSessionClosed api.HubSessionResult

func (r hubSessionClosed) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s the %s session with node %s, %s\n", st.Success("closed"), r.Session.Direction, st.Name(r.Session.Node.Name), st.Muted(r.Session.Id))
}

type hubSessionsClosed api.HubSessionsClosed

func (r hubSessionsClosed) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s %d sessions\n", st.Success("closed"), len(r.Sessions))
	for _, s := range r.Sessions {
		fmt.Fprintf(w, "  %s with node %s, %s\n", s.Direction, st.Name(s.Node.Name), st.Muted(s.Id))
	}
}

// completeHubSessions completes --id with the sessions open; nothing when
// the daemon does not answer in time.
func completeHubSessions(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), completionWait(cmd))
	defer cancel()
	list, err := clientOf(cmd).HubSessions(ctx)
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no sessions to complete", "err", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []cobra.Completion
	for _, s := range list.Sessions {
		completions = append(completions, cobra.CompletionWithDesc(s.Id, string(s.Direction)+" "+s.Node.Name))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
