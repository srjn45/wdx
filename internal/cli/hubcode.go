package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/boombuler/barcode/qr"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

// codeTries is how many codes setup asks for before it gives up.
const codeTries = 3

func newHubCodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "code",
		Short: "Set up this node's authenticator code, which other nodes need to open a session into it",
		Long: "Only this node checks its code, so each node that allows remote access has\n" +
			"an authenticator entry of its own, set up while at it. Both commands are\n" +
			"interactive, and have no --json: the secret opens sessions into the node, so\n" +
			"it is never printed where an AI's transcript would keep it.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHubCodeSetupCmd(), newHubCodeRecoverCmd())
	return cmd
}

// The errors of setting a code up, besides those of how it started.
var codeSetupErrors = []api.ErrorCode{api.ErrorCodeCodeWrong, api.ErrorCodeNotLoggedIn, api.ErrorCodeDaemonUnavailable}

const replaceCodeQuestion = "replace authenticator code"

func newHubCodeSetupCmd() *cobra.Command {
	var replace bool
	cmd := &cobra.Command{
		Use:   "setup [--replace]",
		Short: "Set up an authenticator entry for this node, and get its recovery key",
		Long: "A QR code for your authenticator app is shown, with the secret as text for\n" +
			"an app that cannot scan; you type a code from the app back, three tries at\n" +
			"most, and get a recovery key, shown once. With a code set up already,\n" +
			"--replace replaces it, asking first: the old entry stops working, and the\n" +
			"sessions open stay. Sessions also need hub.remote_access on.",
		Example: clikit.Examples(
			"Set up this node's code", identity.Name+" hub code setup",
			"Replace it with a new entry", identity.Name+" hub code setup --replace",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "no code set up",
			clikit.OutputNote:    "The entry's label and the recovery key, once. Interactive only: no --json.",
			clikit.Results:       string(api.ResultSetUp),
			clikit.Errors:        clikit.CodeList(append([]api.ErrorCode{api.ErrorCodeCancelled, api.ErrorCodeCodeExists}, codeSetupErrors...)...),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := interactiveOnly(cmd); err != nil {
				return err
			}
			c := clientOf(cmd)
			answers := bufio.NewScanner(cmd.InOrStdin())
			if replace {
				if err := confirmCodeReplace(cmd, c, answers); err != nil {
					return err
				}
			}
			pending, err := c.StartHubCode(cmd.Context(), replace)
			if err != nil {
				return codeHint(err)
			}
			return finishCodeSetup(cmd, c, answers, pending.Code)
		},
	}
	cmd.Flags().BoolVar(&replace, "replace", false, "replace the code set up, asking first")
	return cmd
}

func newHubCodeRecoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recover",
		Short: "Set up a new authenticator entry with the recovery key, when the old is lost",
		Long: "You type the recovery key, hidden as you type it; a wrong one makes the next\n" +
			"try wait, from 1 second doubling to 5 minutes. A right one goes on as setup\n" +
			"does, with a new recovery key at the end: the old one is spent.",
		Example: clikit.Examples(
			"Recover this node's code", identity.Name+" hub code recover",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not recovered",
			clikit.OutputNote:    "The entry's label and the new recovery key, once. Interactive only: no --json.",
			clikit.Results:       string(api.ResultRecovered),
			clikit.Errors:        clikit.CodeList(append([]api.ErrorCode{api.ErrorCodeCodeNotSetUp, api.ErrorCodeRecoveryKeyWrong}, codeSetupErrors...)...),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := interactiveOnly(cmd); err != nil {
				return err
			}
			key, err := askHidden(cmd, "recovery key: ")
			if err != nil {
				return err
			}
			c := clientOf(cmd)
			pending, err := c.RecoverHubCode(cmd.Context(), key)
			if err != nil {
				return codeHint(err)
			}
			return finishCodeSetup(cmd, c, bufio.NewScanner(cmd.InOrStdin()), pending.Code)
		},
	}
}

// interactiveOnly refuses a run that cannot ask, or that would put the
// secret in --json.
func interactiveOnly(cmd *cobra.Command) error {
	if reason := clikit.NoPromptReason(cmd); reason != "" {
		return &clikit.UsageError{Err: fmt.Errorf("%s is interactive only (%s): run it in a terminal, so that the secret stays out of logs and transcripts", cmd.CommandPath(), reason)}
	}
	return nil
}

// confirmCodeReplace asks whether to replace the code set up; nil means yes,
// or that none is set up, which there is nothing to ask about.
func confirmCodeReplace(cmd *cobra.Command, c *client.Client, answers *bufio.Scanner) error {
	h, err := c.Hub(cmd.Context())
	if err != nil {
		return err
	}
	if !h.Hub.CodeSetup {
		return nil
	}
	st := clikit.StderrStyle(cmd)
	question := fmt.Sprintf("replace this node's code? the old authenticator entry stops working once the new one is set up; sessions open stay %s: ", st.Command("[y/N]"))
	answer, err := clikit.Ask(cmd.ErrOrStderr(), answers, question, "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		clikit.LogAnswer(cmd, replaceCodeQuestion, "no", "prompt")
		return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you answered no")}
	}
	clikit.LogAnswer(cmd, replaceCodeQuestion, "yes", "prompt")
	return nil
}

// finishCodeSetup shows the pending secret, asks for a code of it until one
// is right, three times at most, and prints the recovery key.
func finishCodeSetup(cmd *cobra.Command, c *client.Client, answers *bufio.Scanner, p api.HubCodePending) error {
	log := logging.FromContext(cmd.Context())
	stderr, st := cmd.ErrOrStderr(), clikit.StderrStyle(cmd)
	fmt.Fprintf(stderr, "scan this with your authenticator app, to add the entry %s:\n\n", st.Name(p.Label))
	if err := writeQR(stderr, p.Url); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "\nor type the secret in it: %s\n\n", st.Name(p.Secret))
	var err error
	for try := 1; try <= codeTries; try++ {
		var code string
		if code, err = clikit.Ask(stderr, answers, "code from the app: "); err != nil {
			return err
		}
		var r api.HubCodeSetUp
		r, err = c.VerifyHubCode(cmd.Context(), code)
		if err == nil {
			log.Info("code verified", "try", try)
			return clikit.PrintResult(cmd, hubCodeResult{Result: r.Result, Label: r.Label, recoveryKey: r.RecoveryKey})
		}
		if problemCode(err) != api.ErrorCodeCodeWrong {
			return err
		}
		log.Info("code wrong", "try", try)
		if try < codeTries {
			fmt.Fprintf(stderr, "%s that is not the entry's code; type the one the app shows now\n", st.Warning("wrong code:"))
		}
	}
	return &clikit.Hinted{Err: err, Hint: "check the app's clock, then run " + cmd.CommandPath() + " again"}
}

// codeHint adds to a code command's failure what to do about it.
func codeHint(err error) error {
	switch problemCode(err) {
	case api.ErrorCodeCodeExists:
		return &clikit.Hinted{Err: err, Hint: "pass --replace to replace it, or run " + identity.Name + " hub code recover if its entry is lost"}
	case api.ErrorCodeCodeNotSetUp:
		return &clikit.Hinted{Err: err, Hint: "run " + identity.Name + " hub code setup"}
	case api.ErrorCodeNotLoggedIn:
		return &clikit.Hinted{Err: err, Hint: "run " + identity.Name + " hub login --url <url> first"}
	}
	return err
}

// askHidden asks question on stderr and reads the answer from the terminal
// without showing it.
func askHidden(cmd *cobra.Command, question string) (string, error) {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return "", errors.New("stdin is not a terminal")
	}
	fmt.Fprint(cmd.ErrOrStderr(), question)
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("the answer could not be read: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// writeQR draws a QR code of content with half-block characters, two of its
// rows to a line, with a quiet zone around it. Its light modules are drawn
// as blocks, for a terminal's dark background.
func writeQR(w io.Writer, content string) error {
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return err
	}
	const quiet = 2
	size := code.Bounds().Dx()
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		if x < 0 || y < 0 || x >= size || y >= size {
			return false
		}
		r, _, _, _ := code.At(x, y).RGBA()
		return r == 0
	}
	var b strings.Builder
	for y := 0; y < size+2*quiet; y += 2 {
		for x := range size + 2*quiet {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case !top && !bottom:
				b.WriteRune('█')
			case !top:
				b.WriteRune('▀')
			case !bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// hubCodeResult is a code set up. The recovery key is printed, never
// logged: as an unexported field, it is in neither the JSON nor the log
// line of the response.
type hubCodeResult struct {
	Result      api.Result `json:"result"`
	Label       string     `json:"label"`
	recoveryKey string
}

func (r hubCodeResult) Text(w io.Writer, st style.Style) {
	what := "set up"
	if r.Result == api.ResultRecovered {
		what = "recovered"
	}
	fmt.Fprintf(w, "%s the authenticator code of this node, as the entry %s\n", st.Success(what), st.Name(r.Label))
	fmt.Fprintf(w, "recovery key: %s\n", st.Name(r.recoveryKey))
	fmt.Fprintf(w, "%s write this down: it is the only way to set up a new authenticator here\n", st.Warning("warning:"))
}

func newHubAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Check the node's record of access to it",
		Long: "The node keeps a tamper-evident chain of what matters about access to it:\n" +
			"its code set up, replaced or recovered, wrong recovery keys, and remote\n" +
			"access turned on or off.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHubAuditVerifyCmd())
	return cmd
}

func newHubAuditVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check that no row of the node's audit chain was changed, removed or inserted",
		Long: "The daemon walks the chain in order, recomputing each row's hash, and stops\n" +
			"at the first that does not match. It works logged out.",
		Example: clikit.Examples(
			"Verify the audit chain", identity.Name+" hub audit verify",
			"As JSON", identity.Name+" hub audit verify --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not verified",
			clikit.OutputNote: "How many rows the chain has and the last one's hash.\n" +
				`With --json: {"audit": {"rows": ..., "head": ...}}`,
			clikit.Errors: clikit.CodeList(api.ErrorCodeAuditBroken, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).VerifyHubAudit(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, hubAuditResult(r))
		},
	}
}

type hubAuditResult api.HubAuditEnvelope

func (r hubAuditResult) Text(w io.Writer, st style.Style) {
	if r.Audit.Head == nil {
		fmt.Fprintf(w, "%s the node's audit chain: it has no rows yet\n", st.Success("verified"))
		return
	}
	fmt.Fprintf(w, "%s the node's audit chain: %d rows\n", st.Success("verified"), r.Audit.Rows)
	clikit.FieldPrinter(w, st, 4)("head", *r.Audit.Head)
}
