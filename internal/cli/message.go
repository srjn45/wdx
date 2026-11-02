package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newMessageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "See the messages still waiting to reach a terminal or agent",
		Long: "wdx agent send and wdx terminal send keep a message until its holder says it\n" +
			"typed it. When a holder dies, its messages go on if the agent resumes the same\n" +
			"conversation. If it starts anew, or a shell does, they are held for you to send\n" +
			"or discard, as what they were meant for is gone.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newMessageDiscardCmd(), newMessageListCmd(), newMessageResendCmd())
	return cmd
}

func newMessageListCmd() *cobra.Command {
	var scope projectScope
	cmd := &cobra.Command{
		Use:   "list [--project-id <id> | --project-name <name>]",
		Short: "List a project's messages still waiting, oldest first",
		Long:  "Without a project flag, the project the working directory is in.",
		Example: examples(
			"The messages of the project you are in", identity.Name+" message list",
			"Another project's, as JSON", identity.Name+" message list --project-name app --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The messages, oldest first.\n" + `With --json: {"messages": [...]}`,
			errorList:  codeList(api.ErrorCodeNotFound, api.ErrorCodeInvalidPath, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, dir, err := worktreeScopeOf(cmd, scope)
			if err != nil {
				return err
			}
			list, err := clientOf(cmd).ListMessages(cmd.Context(), ref, dir)
			if err != nil {
				return outsideHint(err, scope, "")
			}
			return printResult(cmd, messageList(list))
		},
	}
	scope.addFlags(cmd)
	return cmd
}

func newMessageResendCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "resend --id <id>",
		Short: "Send a message still waiting to its terminal or agent, as it is now",
		Long:  "It goes in whatever conversation the agent is in now.",
		Example: examples(
			"Send a held message", identity.Name+" message resend --id msg-1a2b3c4d",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not sent",
			outputNote:    "The message.\n" + `With --json: {"result": "queued", "message": {...}}`,
			resultList:    "queued",
			errorList:     codeList(api.ErrorCodeNotFound, api.ErrorCodeAgentNotRunning, api.ErrorCodeTerminalNotRunning, api.ErrorCodeSendFailed, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).ResendMessage(cmd.Context(), id)
			if err != nil {
				return err
			}
			return printResult(cmd, messageResult(r))
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "`id` of the message")
	cmd.MarkFlagRequired("id")
	completeFlag(cmd, "id", cobra.NoFileCompletions)
	return cmd
}

func newMessageDiscardCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "discard --id <id>",
		Short: "Discard a message still waiting",
		Long:  "A review's comments go back to being drafts, to send again.",
		Example: examples(
			"Discard a held message", identity.Name+" message discard --id msg-1a2b3c4d",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			failurePrefix: "not discarded",
			outputNote:    "That it went.\n" + `With --json: {"result": "removed"}`,
			resultList:    "removed",
			errorList:     codeList(api.ErrorCodeNotFound, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).DiscardMessage(cmd.Context(), id)
			if err != nil {
				return err
			}
			return printResult(cmd, messageDiscarded(r))
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "`id` of the message")
	cmd.MarkFlagRequired("id")
	completeFlag(cmd, "id", cobra.NoFileCompletions)
	return cmd
}

type messageList api.MessageList

func (l messageList) text(w io.Writer, st style.Style) {
	if len(l.Messages) == 0 {
		fmt.Fprintln(w, "no messages waiting")
		return
	}
	var table style.Table
	table.Row(st.Heading("ID"), st.Heading("TO"), st.Heading("STATE"), st.Heading("TEXT"))
	for _, m := range l.Messages {
		table.Row(st.Muted(m.Id), st.Name(m.Name), string(m.State), m.Text)
	}
	table.Write(w)
}

type messageResult api.MessageResult

func (r messageResult) text(w io.Writer, st style.Style) {
	fmt.Fprintln(w, st.Success("queued")+" for "+st.Name(r.Message.Name))
}

type messageDiscarded api.MessageDiscarded

func (messageDiscarded) text(w io.Writer, st style.Style) {
	fmt.Fprintln(w, st.Success("discarded"))
}

func loggedMessage(v any) slog.Value {
	b, err := json.Marshal(v)
	if err != nil {
		return slog.StringValue("unreadable response")
	}
	return slog.AnyValue(api.WorktreeResponseLogged(b))
}

// LogValue keeps what messages say out of the log.
func (l messageList) LogValue() slog.Value { return loggedMessage(l) }

// LogValue keeps what the message says out of the log.
func (r messageResult) LogValue() slog.Value { return loggedMessage(r) }
