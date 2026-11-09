package hubcli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/hub/hubconfig"
	"github.com/srjn45/wdx/internal/style"
)

// privilegedPortFlag gives the service the capability to listen on a port
// below 1024, as the web endpoint serving TLS itself on 443 does.
const privilegedPortFlag = "privileged-port"

// newServiceCmd is the systemd service of the installed hub: install-hub.sh
// and wdx-hub update write the unit it prints, so that each release brings
// its own. Hidden, as people never run it.
func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "service",
		Short:  "The systemd service of the installed hub",
		Hidden: true,
		Args:   clikit.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newServiceUnitCmd())
	return cmd
}

func newServiceUnitCmd() *cobra.Command {
	var privileged bool
	cmd := &cobra.Command{
		Use:   "unit",
		Short: "Print the systemd unit install-hub.sh and " + name + " update install",
		Long: "The unit runs " + hubconfig.Installed.Binary + " serve as the user " + hubconfig.Installed.User + ", whose home holds\n" +
			"the hub's, with the filesystem read-only but for it and no capability but,\n" +
			"with --" + privilegedPortFlag + ", the one to listen on a port below 1024.",
		Example: clikit.Examples(
			"Print the unit", name+" service unit",
			"Print the unit of a hub serving TLS itself on port 443", name+" service unit --"+privilegedPortFlag,
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The unit file.\n" +
				`With --json: {"unit": ...}`,
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clikit.PrintResult(cmd, serviceUnit{Unit: unit(hubconfig.Installed, privileged)})
		},
	}
	cmd.Flags().BoolVar(&privileged, privilegedPortFlag, false, "let the hub listen on a port below 1024, such as 443 for web.tls files")
	return cmd
}

type serviceUnit struct {
	Unit string `json:"unit"`
}

func (u serviceUnit) Text(w io.Writer, _ style.Style) { fmt.Fprint(w, u.Unit) }

// unit is the systemd unit of the hub installed in l, with the capability to
// listen on a port below 1024 when privileged.
func unit(l hubconfig.Layout, privileged bool) string {
	capabilities := "CapabilityBoundingSet=\n"
	if privileged {
		capabilities = "CapabilityBoundingSet=CAP_NET_BIND_SERVICE\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `# wdx-hub as a system service, as install-hub.sh installs it: %[1]s service unit
# prints this file, and %[1]s update writes it anew from each release.
[Unit]
Description=wdx hub
Documentation=https://github.com/srjn45/wdx/tree/main/deploy/hub
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=%[2]s serve
User=%[3]s
Group=%[3]s
# The user's home directory is /var/lib/%[1]s, and the hub's home the
# default in it, /var/lib/%[1]s/.%[1]s. Its settings are in config.yaml
# there, written by sudo %[1]s setting set; one given here as %[4]s<KEY>
# wins over the file.
StateDirectory=%[1]s
StateDirectoryMode=0700
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
%[5]sRestart=on-failure

[Install]
WantedBy=multi-user.target
`, name, l.Binary, l.User, hubconfig.EnvPrefix, capabilities)
	return b.String()
}
