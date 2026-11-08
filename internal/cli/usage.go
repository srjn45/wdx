package cli

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/style"
)

func newUsageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Show how much of each AI coding agent's account's limits is used",
		Long: "As each AI coding agent last told through one of its agents: a row per window\n" +
			"of its limits and bucket of models, with the window's key and the bucket's\n" +
			"name as the AI coding agent gives them, when it resets and when that was\n" +
			"told. Usage is the account's, not an agent's: every agent of one AI coding\n" +
			"agent on this machine draws on the same one. A window used 100% or more is\n" +
			"limited, and a bucket can be used while none of its windows is. Nothing is\n" +
			"shown until an AI coding agent has told something; one on a plan without\n" +
			"limits never does.",
		Example: examples(
			"Every limit", identity.Name+" usage",
			"As JSON, for a script", identity.Name+" usage --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Each AI coding agent: whether it can be used, then its windows: label, bucket, used,\n" +
				"when it resets and as of when.\n" +
				`With --json: {"usage": [{"aca": ..., "window": ..., "bucket": ..., "used_percent": ..., "resets_at": ..., "limited": ..., "observed_at": ...}, ...]}`,
			errorList: codeList(api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := clientOf(cmd).ListUsage(cmd.Context())
			if err != nil {
				return err
			}
			return printResult(cmd, usageList{UsageList: list, now: time.Now()})
		},
	}
}

// usageList is the usage rows, with when they are read for the time left.
type usageList struct {
	api.UsageList
	now time.Time
}

func (l usageList) text(w io.Writer, st style.Style) {
	if len(l.Usage) == 0 {
		fmt.Fprintln(w, "no usage told yet: no AI coding agent has answered, or its plan has no limits")
		return
	}
	var table style.Table
	for _, b := range api.UsageBuckets(l.Usage) {
		availability := st.Success(api.AvailabilityText(b, l.now))
		if !b.Available {
			availability = st.Failure(api.AvailabilityText(b, l.now))
		}
		name := st.Name(b.Aca)
		if b.Bucket != "" {
			name = st.Name(b.Aca) + " " + b.Bucket
		}
		table.Row(name, availability)
		table.Row(st.Heading("  WINDOW"), st.Heading("USED"), st.Heading("RESETS"), st.Heading("AS OF"))
		for _, u := range b.Windows {
			used := strconv.Itoa(u.UsedPercent) + "%"
			if u.Limited {
				used = st.Failure(used + " limited")
			}
			resets := st.Muted("none")
			if u.ResetsAt != nil {
				resets = api.ResetText(*u.ResetsAt, l.now)
			}
			table.Row("  "+api.UsageLabel(u.Aca, u.Window), used, resets, u.ObservedAt.Local().Format("15:04"))
		}
	}
	table.Write(w)
}
