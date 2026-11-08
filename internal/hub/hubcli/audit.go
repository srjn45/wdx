package hubcli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hubapi"
	"github.com/srjn45/wdx/internal/style"
)

func newAuditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Check the record of every act on the accounts",
		Long: "Every act on an account, by its user or by the admin, is a row of the audit\n" +
			"chain, each row's hash chaining it to the one before.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newAuditVerifyCmd())
	return cmd
}

func newAuditVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check that no row of the audit chain was changed, removed or inserted",
		Long: "The hub walks the chain in order, recomputing each row's hash, and stops at\n" +
			"the first that does not match. A chain rewritten whole matches: compare the\n" +
			"head with the one the hub logs as audit chain head at startup and every hour.",
		Example: clikit.Examples(
			"Verify the audit chain", name+" audit verify",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not verified",
			clikit.OutputNote: "How many rows the chain has and the last one's hash.\n" +
				`With --json: {"audit": {"rows": ..., "head": ...}}`,
			clikit.Errors: clikit.CodeList(hubapi.ErrorCodeHubUnavailable, hubapi.ErrorCodeAuditBroken),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).verifyAudit(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, auditResult(r))
		},
	}
}

type auditResult hubapi.AuditEnvelope

func (r auditResult) Text(w io.Writer, st style.Style) {
	if r.Audit.Head == nil {
		fmt.Fprintf(w, "%s the audit chain: it has no rows yet\n", st.Success("verified"))
		return
	}
	fmt.Fprintf(w, "%s the audit chain: %d rows\n", st.Success("verified"), r.Audit.Rows)
	clikit.FieldPrinter(w, st, 4)("head", *r.Audit.Head)
}
