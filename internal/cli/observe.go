package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/srjn45/warden/internal/api"
	"github.com/srjn45/warden/internal/identity"
	"github.com/srjn45/warden/internal/observe"
	"github.com/srjn45/warden/internal/style"
)

func newObserveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Run Grafana with the logs and metrics",
		Long: "Run Grafana with " + identity.Name + "'s logs and the daemon's metrics in a container on\n" +
			"this machine, under Docker or Podman. It reads the log files of the home,\n" +
			"and the daemon sends its metrics to it at otel.endpoint.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newObserveStartCmd(), newObserveStopCmd(), newObserveStatusCmd())
	return cmd
}

// stackView describes the stack's container.
type stackView struct {
	Runtime      string `json:"runtime"`
	Container    string `json:"container"`
	Image        string `json:"image"`
	Status       string `json:"status"` // as the runtime says: running, exited, ...
	GrafanaURL   string `json:"grafana_url"`
	OTLPEndpoint string `json:"otlp_endpoint"`
	Logs         string `json:"logs"` // the logs folder it reads
}

func newStackView(s *observe.Stack, state observe.State) *stackView {
	return &stackView{
		Runtime:      s.Runtime,
		Container:    observe.Container,
		Image:        state.Image,
		Status:       state.Status,
		GrafanaURL:   s.GrafanaURL(),
		OTLPEndpoint: s.OTLPEndpoint(),
		Logs:         state.Logs,
	}
}

func (v *stackView) text(w io.Writer, st style.Style) {
	field := func(name, value string) {
		fmt.Fprintf(w, "  %s %s\n", st.Muted(fmt.Sprintf("%-8s", name)), value)
	}
	field("grafana:", st.Hint(v.GrafanaURL))
	field("metrics:", v.OTLPEndpoint)
	field("logs:", v.Logs)
	field("runtime:", fmt.Sprintf("%s, container %s %s", v.Runtime, v.Container, st.Muted("("+v.Image+")")))
}

// findStack returns the stack of the home, run by the runtime found.
func findStack(cmd *cobra.Command) (*observe.Stack, error) {
	env := invocationOf(cmd).env
	ports := env.ObservePorts
	if ports == (observe.Ports{}) {
		ports = observe.DefaultPorts
	}
	lookPath := env.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return observe.Find(cmd.Context(), lookPath, env.Config.Home, ports)
}

// stackState returns the stack's container, or nil when there is none.
func stackState(cmd *cobra.Command, s *observe.Stack) (*stackView, error) {
	state, err := s.State(cmd.Context())
	if err != nil || state == nil {
		return nil, err
	}
	return newStackView(s, *state), nil
}

// observeResult is what starting or stopping the stack did.
type observeResult struct {
	Result      string     `json:"result"`
	Stack       *stackView `json:"stack"`        // the stack running; null once stopped
	DataRemoved *bool      `json:"data_removed"` // whether stop --purge removed data; null without it
}

func (r observeResult) text(w io.Writer, st style.Style) {
	switch r.Result {
	case "started":
		fmt.Fprintf(w, "observability stack %s: open %s\n", st.Success("started"), st.Hint(r.Stack.GrafanaURL))
		r.Stack.text(w, st)
	case "running":
		fmt.Fprintf(w, "observability stack %s: open %s\n", st.Warning("already running"), st.Hint(r.Stack.GrafanaURL))
		r.Stack.text(w, st)
	case "stopped":
		fmt.Fprintf(w, "observability stack %s\n", st.Success("stopped"))
	default:
		fmt.Fprintf(w, "observability stack %s\n", st.Warning("not running"))
	}
	switch {
	case r.DataRemoved == nil && r.Result == "stopped":
		fmt.Fprintf(w, "  its data stays for the next start; %s removes it\n", st.Command(identity.Name+" observe stop --purge"))
	case r.DataRemoved != nil && *r.DataRemoved:
		fmt.Fprintln(w, "  its data was removed")
	case r.DataRemoved != nil:
		fmt.Fprintln(w, "  it had no data")
	}
}

func newObserveStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start Grafana with the logs and metrics",
		Long: "Start the observability stack and wait until it is ready: the\n" +
			"grafana/otel-lgtm image, with Grafana on 127.0.0.1:3000 and the collector\n" +
			"the daemon sends its metrics to on 127.0.0.1:4318. The first start pulls\n" +
			"the image, which can take minutes. It runs until stopped, and its data stays\n" +
			"across starts.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "The stack, already running or started, and where to open Grafana.\n" +
				`With --json: {"result": ..., "stack": {...}, "data_removed": null}`,
			resultList: annotation("running", "started"),
			errorList:  codeList(api.ErrorCodePortInUse, api.ErrorCodeRuntimeFailed, api.ErrorCodeRuntimeMissing),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := findStack(cmd)
			if err != nil {
				return err
			}
			v, err := stackState(cmd, s)
			if err != nil {
				return err
			}
			if v != nil && v.Status == "running" {
				return printResult(cmd, observeResult{Result: "running", Stack: v})
			}
			progress := io.Discard
			if !jsonMode(cmd) {
				progress = cmd.ErrOrStderr()
			}
			if err := s.Start(cmd.Context(), progress); err != nil {
				return err
			}
			if v, err = stackState(cmd, s); err != nil {
				return err
			}
			return printResult(cmd, observeResult{Result: "started", Stack: v})
		},
	}
}

func newObserveStopCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "stop [--purge]",
		Short: "Stop Grafana",
		Long: "Stop the observability stack and remove its container. Its data, the logs and\n" +
			"metrics it stored, stays for the next start unless --purge is given.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Whether the stack was stopped and, with --purge, whether its data was removed.\n" +
				`With --json: {"result": ..., "stack": null, "data_removed": ...}`,
			resultList: annotation("not_running", "stopped"),
			errorList:  codeList(api.ErrorCodeRuntimeFailed, api.ErrorCodeRuntimeMissing),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := findStack(cmd)
			if err != nil {
				return err
			}
			removed, err := s.Remove(cmd.Context())
			if err != nil {
				return err
			}
			r := observeResult{Result: "not_running"}
			if removed {
				r.Result = "stopped"
			}
			if purge {
				dataRemoved, err := s.RemoveData(cmd.Context())
				if err != nil {
					return err
				}
				r.DataRemoved = &dataRemoved
			}
			return printResult(cmd, r)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the stack's data")
	return cmd
}

// observeStatus is whether the stack is running.
type observeStatus struct {
	Running bool       `json:"running"`
	Runtime *string    `json:"runtime"` // null when neither Docker nor Podman is installed
	Stack   *stackView `json:"stack"`   // null when there is no container
}

func (s observeStatus) text(w io.Writer, st style.Style) {
	switch {
	case s.Runtime == nil:
		fmt.Fprintf(w, "observability stack %s: neither docker nor podman is installed\n", st.Warning("not running"))
		return
	case s.Running:
		fmt.Fprintf(w, "observability stack %s: open %s\n", st.Success("running"), st.Hint(s.Stack.GrafanaURL))
	case s.Stack != nil:
		fmt.Fprintf(w, "observability stack %s %s\n", st.Warning("not running"), st.Muted("(container "+s.Stack.Status+")"))
	default:
		fmt.Fprintf(w, "observability stack %s\n", st.Warning("not running"))
		return
	}
	s.Stack.text(w, st)
}

func newObserveStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether Grafana is running",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			outputNote: "Whether the stack is running, under which runtime, and its container: where to open\n" +
				"Grafana, where it takes metrics and which logs it reads.\n" +
				`With --json: {"running": ..., "runtime": ..., "stack": {...}}`,
			errorList: codeList(api.ErrorCodeRuntimeFailed),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := findStack(cmd)
			if _, missing := errors.AsType[*observe.RuntimeMissingError](err); missing {
				return printResult(cmd, observeStatus{})
			}
			if err != nil {
				return err
			}
			v, err := stackState(cmd, s)
			if err != nil {
				return err
			}
			return printResult(cmd, observeStatus{Running: v != nil && v.Status == "running", Runtime: &s.Runtime, Stack: v})
		},
	}
}
