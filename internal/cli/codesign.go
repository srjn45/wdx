package cli

import (
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/codesign"
	"github.com/srjn45/wdx/internal/service"
	"github.com/srjn45/wdx/internal/style"
)

// newCodesignCmd signs a binary on macOS, for the install script, which
// runs the release it downloaded to sign a copy of itself: the wdx doc
// MAC_SIGNING. Hidden, as people never run it.
func newCodesignCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:    "codesign --path <file>",
		Short:  "Sign a binary on macOS, so the permissions macOS grants it survive updates; the install script runs it",
		Hidden: true,
		Args:   cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The path signed, and whether the certificate was made.\n" +
				`With --json: {"result": "signed", "path": ..., "certificate_made": ...}`,
			resultList:    annotation(resultSigned),
			failurePrefix: "not signed",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !codesign.Supported(runtime.GOOS) {
				return &usageError{errors.New("signing is for macOS only")}
			}
			abs, err := absolute(cmd, path)
			if err != nil {
				return &usageError{err}
			}
			r, err := codesign.Sign(cmd.Context(), invocationOf(cmd).env.Config.Home, abs, service.Label)
			if err != nil {
				return err
			}
			return printResult(cmd, codesignResult{Result: resultSigned, Path: abs, CertificateMade: r.CertificateMade})
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "`file` to sign in place")
	cmd.MarkFlagRequired("path")
	completeFlag(cmd, "path", completeFiles)
	return cmd
}

type codesignResult struct {
	Result          string `json:"result"`
	Path            string `json:"path"`
	CertificateMade bool   `json:"certificate_made"`
}

func (r codesignResult) text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s %s\n", st.Success("signed"), st.Name(r.Path))
	if r.CertificateMade {
		fmt.Fprintf(w, "made the %s certificate: a new identity, which macOS must be granted Full Disk Access once\n", st.Name(codesign.Certificate))
	}
}
