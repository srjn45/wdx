package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// nodeFlag runs a command on another node of the account, through a session:
// a persistent flag of the groups whose commands the daemon serves.
const nodeFlag = "node"

// remoteMark marks the --node flag that runs a command on another node, as
// against the --node of the hub's commands, which names the node they act on.
const remoteMark = "remote"

// noNode is the annotation of a command that refuses --node, with the reason.
const noNode = "noNode"

// nodeCodes are the codes every command may return with --node, besides
// its own: reaching the node, the session the code opens, and the host's
// rules.
var nodeCodes = []api.ErrorCode{
	api.ErrorCodeNotLoggedIn, api.ErrorCodeNodeNotFound, api.ErrorCodeNodeOffline, api.ErrorCodeRemoteAccessOff,
	api.ErrorCodeCodeNotSetUp, api.ErrorCodeCodeWrong, api.ErrorCodeNodeKeyMismatch, api.ErrorCodeTooManyStreams,
	api.ErrorCodeHubUnreachable, api.ErrorCodeHubVersionUnsupported, api.ErrorCodeNodeVersionUnsupported,
	api.ErrorCodeRouteNotAllowed, api.ErrorCodeSessionRequired, api.ErrorCodeSessionClosed, api.ErrorCodeRemoteNotForAgents,
	api.ErrorCodeDaemonUnavailable,
}

// addNodeFlag gives cmd and the commands under it --node.
func addNodeFlag(cmd *cobra.Command) *cobra.Command {
	cmd.PersistentFlags().String(nodeFlag, "", "run the command on the node of this `name`, another of your account, through a session")
	_ = cmd.PersistentFlags().SetAnnotation(nodeFlag, remoteMark, []string{"true"}) // the flag was just added
	clikit.FlagErrors(cmd, nodeFlag, nodeCodes...)
	clikit.CompleteFlag(cmd, nodeFlag, completeHubNodes("name"))
	return cmd
}

// nodeOf is the node the command line runs cmd on, "" for this one.
func nodeOf(cmd *cobra.Command) string {
	f := cmd.Flag(nodeFlag)
	if f == nil || f.Annotations[remoteMark] == nil {
		return ""
	}
	return f.Value.String()
}

// wrapNode makes every command under cmd that has --node check, when it is
// given, what may run on another node before it runs, write on which to
// stderr, and add to its failure what to do about it. The commands
// themselves do not know they run remotely.
func wrapNode(cmd *cobra.Command) {
	if run := cmd.RunE; run != nil && !cmd.HasSubCommands() && cmd.Flag(nodeFlag) != nil {
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			node := nodeOf(cmd)
			if node == "" {
				return run(cmd, args)
			}
			if err := nodeAllowed(cmd); err != nil {
				return err
			}
			if !clikit.JSONMode(cmd) {
				fmt.Fprintln(cmd.ErrOrStderr(), clikit.StderrStyle(cmd).Remote("on node "+node))
			}
			return nodeHint(run(cmd, args), node)
		}
	}
	for _, sub := range cmd.Commands() {
		wrapNode(sub)
	}
}

// nodeAllowed fails when cmd may not run on another node: from an agent,
// whatever the command, before anything is asked; or a command that refuses
// --node.
func nodeAllowed(cmd *cobra.Command) error {
	if id := envOf(cmd).AgentID; id != "" {
		logging.FromContext(cmd.Context()).Info("node refused", "reason", "an agent runs the command", "agent_id", id)
		p := api.NewProblem(api.ErrorCodeRemoteNotForAgents, "an agent cannot run a command on another node: WDX_AGENT_ID is set, to "+id)
		p.Hint = ptr("run the command on this node, without --node")
		return &client.ProblemError{Problem: p}
	}
	if reason := cmd.Annotations[noNode]; reason != "" {
		return &clikit.UsageError{Err: errors.New(reason)}
	}
	return nil
}

// nodeHint adds to a failure through a session what to do about it.
func nodeHint(err error, node string) error {
	var p api.Problem
	if e, ok := errors.AsType[*client.ProblemError](err); ok {
		p = e.Problem
	}
	if _, hinted := errors.AsType[*clikit.Hinted](err); hinted || p.Hint != nil {
		return err
	}
	update := ""
	if p.Details != nil {
		update, _ = (*p.Details)["update"].(string)
	}
	switch {
	case p.Code == api.ErrorCodeSessionClosed:
		return &clikit.Hinted{Err: err, Hint: "run the command again, which asks for the code of " + node + " to open another"}
	case p.Code == api.ErrorCodeNodeVersionUnsupported && update == "client":
		return &clikit.Hinted{Err: err, Hint: "update wdx here: " + identity.Name + " update"}
	case p.Code == api.ErrorCodeNodeVersionUnsupported, p.Code == api.ErrorCodeRouteNotAllowed && update == "host":
		return &clikit.Hinted{Err: err, Hint: "update wdx on " + node + ": " + identity.Name + " update there"}
	}
	return err
}

// remoteClient is the client of node's daemon, through the session this
// node's daemon has open into it. A connection opens with the code, asked
// for on a terminal, when no session is open. dialed, unless nil, is told
// what each stream into node met.
func remoteClient(cmd *cobra.Command, node string, dialed func(error)) *client.Client {
	inv := clikit.InvocationOf(cmd)
	local := localClientOf(cmd)
	var opened atomic.Bool
	return client.New(client.Options{
		DebugID: inv.DebugID,
		Quiet:   inv.Quiet,
		Node:    node,
		Dial: func(ctx context.Context) (net.Conn, error) {
			conn, err := dialNode(ctx, cmd, local, node, opened.Load())
			if err == nil {
				opened.Store(true)
			}
			if dialed != nil {
				dialed(err)
			}
			return conn, err
		},
	})
}

// dialNode opens a stream into node through local, opening a session with
// the code first when there is none and one can be asked for. Once a stream
// opened, no session means the one the command ran in ended: it is not
// opened again behind the command's back.
func dialNode(ctx context.Context, cmd *cobra.Command, local *client.Client, node string, opened bool) (net.Conn, error) {
	if err := nodeAllowed(cmd); err != nil {
		return nil, err
	}
	conn, err := local.Connect(ctx, node)
	if problemCode(err) != api.ErrorCodeSessionRequired {
		return conn, err
	}
	log := logging.FromContext(ctx)
	if opened {
		log.Info("session ended while the command ran", "node", node)
		p := api.NewProblem(api.ErrorCodeSessionClosed, "the session into node "+node+" ended while the command ran")
		details := map[string]any{"node": node}
		if e, ok := errors.AsType[*client.ProblemError](err); ok && e.Problem.Details != nil {
			if reason, ok := (*e.Problem.Details)["reason"].(string); ok {
				details["reason"] = reason // as the host said, when it just closed it
			}
		}
		p.Details = &details
		return nil, &client.ProblemError{Problem: p}
	}
	if err := notThisNode(ctx, local, node, "run the command without --node"); err != nil {
		return nil, err
	}
	if clikit.InvocationOf(cmd).Quiet || !clikit.CanPrompt(cmd) {
		if !clikit.InvocationOf(cmd).Quiet {
			clikit.LogUnanswered(cmd, "code for "+node)
		}
		return nil, &clikit.Hinted{Err: err, Hint: "open one with the code of " + node + ": " + identity.Name + " hub session open --node " + node + " --code <code>"}
	}
	log.Info("session asked for", "node", node)
	code, err := askCode(cmd, node)
	if err != nil {
		return nil, err
	}
	if _, err := local.OpenHubSession(ctx, node, strings.TrimSpace(code)); err != nil {
		return nil, sessionHint(err, node)
	}
	return local.Connect(ctx, node)
}

// notThisNode fails with a usage error when node names this machine, as
// the daemon's hub status names it: no session opens into the node itself,
// so no code is asked for one. what says what to do instead. When the
// status cannot be read, the request that follows says why.
func notThisNode(ctx context.Context, local *client.Client, node, what string) error {
	h, err := local.Hub(ctx)
	if err != nil || h.Hub.Node == nil {
		return nil
	}
	this := h.Hub.Node
	if !strings.EqualFold(this.Name, node) && "id:"+this.Id != node {
		return nil
	}
	logging.FromContext(ctx).Info("node refused", "reason", "it is this machine", "node", node, "node_id", this.Id)
	return &clikit.UsageError{Err: fmt.Errorf("%s is this machine: %s", node, what)}
}

// daemonPath makes path absolute for the daemon that receives it: against
// the working directory here, or with --node as it is, absolute on that
// node, where this directory means nothing.
func daemonPath(cmd *cobra.Command, path string) (string, error) {
	node := nodeOf(cmd)
	if node == "" || path == "" {
		return clikit.Absolute(cmd, path)
	}
	if !filepath.IsAbs(path) {
		return "", &clikit.UsageError{Err: fmt.Errorf("path must be absolute on node %s: %s", node, path)}
	}
	return filepath.Clean(path), nil
}
