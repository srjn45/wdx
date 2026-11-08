package cli

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/client"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/deps"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

// Results of the hub's commands.
const (
	resultLoggedIn  = string(api.ResultLoggedIn)
	resultLoggedOut = string(api.ResultLoggedOut)
)

// callbackPath is where the CLI's loopback listener takes the browser back
// from the hub.
const callbackPath = "/callback"

// openTimeout bounds how long opening the browser may take.
const openTimeout = 10 * time.Second

func newHubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "Make this machine a node of an account on a hub, and see the account's nodes",
		Long: "A hub keeps an account's nodes, the machines its user logged in from. The\n" +
			"daemon keeps a connection to it while logged in, and never waits on it: with\n" +
			"the hub down, everything else works as before.",
		Args: clikit.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHubAuditCmd(), newHubCodeCmd(), newHubLoginCmd(), newHubLogoutCmd(), newHubNodeCmd(), newHubStatusCmd())
	return cmd
}

func newHubLoginCmd() *cobra.Command {
	var url, name string
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login --url <url> [--name <name>] [--no-browser]",
		Short: "Make this machine a node of your account on a hub",
		Long: "The browser opens on the hub's page, where you log in and approve the\n" +
			"machine; the link is printed too, for when none opens. With --no-browser,\n" +
			"or without a browser to open (wdx deps list), the hub's page and a code to\n" +
			"type on it are printed instead, to open in a browser on any device. What to\n" +
			"open, and the code, go to stderr, with --json too. The login waits until it\n" +
			"is approved or denied, at most 10 minutes; Ctrl-C cancels it.\n\n" +
			"The url is https, or plain http to localhost or 127.0.0.1 only.",
		Example: clikit.Examples(
			"Log in, in the browser", identity.Name+" hub login --url https://hub.example.com",
			"Over SSH, with a code typed on another device", identity.Name+" hub login --url https://hub.example.com --no-browser",
			"Name the node, from a script", identity.Name+" hub login --url https://hub.example.com --name laptop --no-browser --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not logged in",
			clikit.OutputNote: "The hub's status once logged in.\n" +
				`With --json: {"result": "logged_in", "hub": {...}}; the link and code still go to stderr`,
			clikit.Results: resultLoggedIn,
			clikit.Errors: clikit.CodeList(api.ErrorCodeAlreadyLoggedIn, api.ErrorCodeHubUnreachable, api.ErrorCodeHubVersionUnsupported,
				api.ErrorCodeInvalidArgument, api.ErrorCodeLoginCancelled, api.ErrorCodeLoginDenied, api.ErrorCodeLoginExpired,
				api.ErrorCodeNameTaken, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHubLogin(cmd, url, name, noBrowser)
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "`url` of the hub, as https://hub.example.com")
	cmd.Flags().StringVar(&name, "name", "", "`name` of this node in the account; the hostname by default")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "open no browser: print the hub's page and a code to type on it")
	cmd.MarkFlagRequired("url")
	clikit.CompleteFlag(cmd, "url", cobra.NoFileCompletions)
	clikit.CompleteFlag(cmd, "name", cobra.NoFileCompletions)
	return cmd
}

// browserDependency is the browser opener, as wdx deps knows it.
func browserDependency() deps.Dependency {
	d, _ := deps.DependencyOf("browser")
	return d
}

func runHubLogin(cmd *cobra.Command, url, name string, noBrowser bool) error {
	ctx := cmd.Context()
	log := logging.FromContext(ctx)
	env := envOf(cmd)
	stderr, st := cmd.ErrOrStderr(), clikit.StderrStyle(cmd)
	if name == "" {
		host, err := os.Hostname()
		if err != nil {
			return &clikit.UsageError{Err: fmt.Errorf("the hostname cannot be read, so pass --name: %w", err)}
		}
		name = host
	}
	flow, opener := api.HubLoginFlowDevice, ""
	if noBrowser {
		log.Info("login by code", "reason", "--no-browser")
	} else if c := deps.CheckOne(ctx, probe(*env), browserDependency()); c.Status == deps.Ready {
		flow, opener = api.HubLoginFlowBrowser, c.Path
	} else {
		log.Info("login by code", "reason", c.Detail)
		fmt.Fprintf(stderr, "%s %s\n", st.Warning("no browser to open:"), c.Detail)
	}
	req := api.HubLoginStart{Url: url, Name: name, Flow: flow}
	c := clientOf(cmd)
	if flow == api.HubLoginFlowBrowser {
		cb, err := listenCallback(cmd)
		if err != nil {
			return err
		}
		defer cb.close()
		req.Callback = &cb.url
	}
	started, err := c.StartHubLogin(ctx, req)
	if err != nil {
		return loginHint(err)
	}
	login := started.Login

	waiting, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	stream, err := c.HubLoginEvents(waiting, login.Id)
	if err != nil {
		return err
	}
	defer stream.Close()
	if login.Flow == api.HubLoginFlowBrowser {
		link := deref(login.LoginUrl)
		fmt.Fprintf(stderr, "opening %s in your browser; if none opens, open it yourself\n", st.Name(link))
		if err := env.openBrowser(ctx, opener, link); err != nil {
			log.Info("browser not opened", "program", opener, "err", err)
			fmt.Fprintf(stderr, "%s %v\n", st.Warning("the browser did not open:"), err)
		}
	} else {
		fmt.Fprintf(stderr, "open %s and type the code %s\n", st.Name(deref(login.VerificationUrl)), st.Name(deref(login.UserCode)))
	}
	fmt.Fprintf(stderr, "waiting for the login to be approved, until %s; Ctrl-C cancels it\n", login.ExpiresAt.Local().Format(time.Kitchen))
	for {
		e, err := stream.Next()
		if err != nil {
			if waiting.Err() != nil {
				return cancelLogin(cmd, c, login.Id)
			}
			if errors.Is(err, io.EOF) {
				err = errors.New("the daemon ended the login's stream before the login ended")
			}
			return err
		}
		switch e.Phase {
		case api.HubLoginEventPhaseLoggedIn:
			log.Info("logged in to the hub", "url", deref(e.Hub.Url), "user", deref(e.Hub.User))
			return clikit.PrintResult(cmd, hubLoginResult{Result: resultLoggedIn, Hub: *e.Hub})
		case api.HubLoginEventPhaseFailed:
			return loginHint(&client.ProblemError{Problem: *e.Problem})
		}
	}
}

// cancelLogin cancels the login of id, as Ctrl-C asked.
func cancelLogin(cmd *cobra.Command, c *client.Client, id string) error {
	log := logging.FromContext(cmd.Context())
	log.Info("login cancelled by a signal")
	// The command is ending: the daemon must hear of it all the same.
	if _, err := c.CancelHubLogin(context.WithoutCancel(cmd.Context()), id); err != nil {
		log.Error("login not cancelled", "err", err)
	}
	return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeLoginCancelled, "you cancelled the login")}
}

// loginHint adds to a login's failure what to do about it.
func loginHint(err error) error {
	hint := ""
	switch problemCode(err) {
	case api.ErrorCodeAlreadyLoggedIn:
		hint = "run " + identity.Name + " hub logout first, or " + identity.Name + " hub status to see the hub"
	case api.ErrorCodeNameTaken:
		hint = "pass another --name"
	case api.ErrorCodeLoginExpired:
		hint = "run it again, and approve it within 10 minutes"
	case api.ErrorCodeHubUnreachable:
		hint = "check --url, and that the hub is up"
	case api.ErrorCodeHubVersionUnsupported:
		hint = "run " + identity.Name + " update, or ask the hub's admin to update it"
	}
	if hint == "" {
		return err
	}
	return &clikit.Hinted{Err: err, Hint: hint}
}

// openBrowser opens link with the opener program, unless the env says how.
func (env *Env) openBrowser(ctx context.Context, program, link string) error {
	if env.OpenBrowser != nil {
		return env.OpenBrowser(ctx, program, link)
	}
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, program, link)
	c.Env = env.Environ
	return c.Run()
}

// callback is the CLI's loopback listener, which the hub sends the browser
// back to with the login's code: it hands the code to the daemon and shows
// the browser a small page.
type callback struct {
	url    string
	server *http.Server
	served chan struct{}
}

func listenCallback(cmd *cobra.Command) (*callback, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	cb := &callback{url: "http://" + ln.Addr().String() + callbackPath, served: make(chan struct{})}
	// The handler's requests to the daemon log as the command does, and run
	// to their end.
	ctx := context.WithoutCancel(cmd.Context())
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+callbackPath, func(w http.ResponseWriter, r *http.Request) {
		serveCallback(ctx, clientOf(cmd), w, r)
	})
	cb.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ErrorLog: slog.NewLogLogger(logging.FromContext(ctx).Handler(), slog.LevelInfo)}
	go func() {
		defer close(cb.served)
		_ = cb.server.Serve(ln) // ErrServerClosed once closed
	}()
	logging.FromContext(ctx).Info("login callback listening", "url", cb.url)
	return cb, nil
}

// close stops listening, once the page being served is.
func (cb *callback) close() {
	_ = cb.server.Shutdown(context.Background())
	<-cb.served
}

// serveCallback takes what the hub sent the browser back with to the daemon:
// the code, a secret it never logs, or the error, and the state.
func serveCallback(ctx context.Context, c *client.Client, w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(ctx)
	q := r.URL.Query()
	code := api.HubLoginCode{State: q.Get("state"), Code: optional(q.Get("code")), Error: optional(q.Get("error"))}
	log.Info("login callback received", "has_code", code.Code != nil, "error", q.Get("error"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if code.State == "" {
		w.WriteHeader(http.StatusBadRequest)
		writePage(w, "This link is not for the login wdx is waiting for.")
		return
	}
	e, err := c.SendHubLoginCode(ctx, code)
	if err != nil {
		log.Info("login callback refused", "code", string(problemCode(err)))
		w.WriteHeader(http.StatusBadRequest)
		writePage(w, "This link is not for the login wdx is waiting for.")
		return
	}
	switch e.Phase {
	case api.HubLoginEventPhaseLoggedIn:
		writePage(w, "Approved: back to your terminal.")
	case api.HubLoginEventPhaseFailed:
		if e.Problem.Code == api.ErrorCodeLoginDenied {
			writePage(w, "Denied: this machine was not added to your account.")
			return
		}
		writePage(w, "The login failed: "+e.Problem.Detail)
	default:
		writePage(w, "Still waiting: back to your terminal.")
	}
}

func writePage(w io.Writer, text string) {
	fmt.Fprintf(w, "<!doctype html>\n<html lang=\"en\"><meta charset=\"utf-8\"><title>wdx</title>\n<p>%s</p>\n</html>\n", html.EscapeString(text))
}

type hubLoginResult struct {
	Result string        `json:"result"`
	Hub    api.HubStatus `json:"hub"`
}

func (r hubLoginResult) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s to %s as %s; this machine is node %s\n", st.Success("logged in"), deref(r.Hub.Url), st.Name(deref(r.Hub.User)), st.Name(r.Hub.Node.Name))
}

func newHubLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Leave the hub: this machine is no longer a node of the account",
		Long: "The hub is asked to revoke this node, then the node's key, certificate and\n" +
			"login are deleted here, whether the hub heard or not: without its key, the\n" +
			"node is of no use to anyone. Projects, agents and docs stay.",
		Example: clikit.Examples(
			"Leave the hub", identity.Name+" hub logout",
			"From a script", identity.Name+" hub logout --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not logged out",
			clikit.OutputNote: "That it left, and whether the hub heard.\n" +
				`With --json: {"result": "logged_out", "hub_told": true|false, "hub": {...}}, the hub as it was`,
			clikit.Results: resultLoggedOut,
			clikit.Errors:  clikit.CodeList(api.ErrorCodeNotLoggedIn, api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).LogoutHub(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, hubLogoutResult(r))
		},
	}
}

type hubLogoutResult api.HubLogoutResult

func (r hubLogoutResult) Text(w io.Writer, st style.Style) {
	name := ""
	if r.Hub.Node != nil {
		name = r.Hub.Node.Name
	}
	fmt.Fprintf(w, "%s of %s; this machine is no longer node %s\n", st.Success("logged out"), deref(r.Hub.Url), st.Name(name))
	if !r.HubTold {
		fmt.Fprintf(w, "%s the hub could not be told; the node stays in the account until it is removed\n", st.Warning("warning:"))
		writeHint(w, st, "remove it from another node: "+identity.Name+" hub node remove --name "+name)
	}
}

// writeHint writes a result's hint, as an error's is written.
func writeHint(w io.Writer, st style.Style, hint string) {
	fmt.Fprintf(w, "  %s %s\n", st.Hint("hint:"), clikit.HintText(st, identity.Name, hint))
}

func newHubStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show this machine's login to a hub and its connection",
		Long: "The connection is connected, connecting, offline (lost, and tried again),\n" +
			"logged_out, revoked (another node removed this one) or disabled (the hub's\n" +
			"admin disabled the account). Not being logged in is a state, not an error.",
		Example: clikit.Examples(
			"Whether this machine is connected", identity.Name+" hub status",
			"As JSON", identity.Name+" hub status --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The state on the first line, then what to do about it.\n" + `With --json: {"hub": {...}}`,
			clikit.Errors:     clikit.CodeList(api.ErrorCodeDaemonUnavailable),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).Hub(cmd.Context())
			if err != nil {
				return err
			}
			return clikit.PrintResult(cmd, hubStatus(r))
		},
	}
}

type hubStatus api.HubEnvelope

func (r hubStatus) Text(w io.Writer, st style.Style) {
	h := r.Hub
	if h.Connection == api.HubConnectionLoggedOut {
		fmt.Fprintln(w, "not logged in to a hub")
		writeHint(w, st, "run "+identity.Name+" hub login --url <url>")
		return
	}
	url, user, node := deref(h.Url), st.Name(deref(h.User)), st.Name(h.Node.Name)
	switch h.Connection {
	case api.HubConnectionConnected:
		fmt.Fprintf(w, "%s to %s as %s, node %s\n", st.Success("connected"), url, user, node)
	case api.HubConnectionConnecting:
		fmt.Fprintf(w, "%s to %s as %s, node %s\n", st.Warning("connecting"), url, user, node)
	case api.HubConnectionOffline:
		fmt.Fprintf(w, "%s: %s cannot be reached; logged in as %s, node %s, and trying again\n", st.Warning("offline"), url, user, node)
	case api.HubConnectionRevoked:
		fmt.Fprintf(w, "%s: another node removed node %s from %s's account on %s\n", st.Failure("revoked"), node, user, url)
		writeHint(w, st, "run "+identity.Name+" hub logout, then "+identity.Name+" hub login to join again")
	case api.HubConnectionDisabled:
		fmt.Fprintf(w, "%s: the hub's admin disabled %s's account on %s\n", st.Failure("disabled"), user, url)
		writeHint(w, st, "ask the hub's admin, or run "+identity.Name+" hub logout")
	}
	if h.HubVersion != nil {
		fmt.Fprintf(w, "%s %s\n", st.Muted("hub version:"), *h.HubVersion)
	}
	access := "remote access off"
	if h.RemoteAccess == api.HubStatusRemoteAccessOn {
		access = "remote access on"
	}
	if h.CodeSetup {
		fmt.Fprintf(w, "%s, code set up\n", access)
	} else {
		fmt.Fprintf(w, "%s, no code set up\n", access)
		writeHint(w, st, "run "+identity.Name+" hub code setup to allow remote access")
	}
	if h.CodeSetup && h.RemoteAccess == api.HubStatusRemoteAccessOff {
		writeHint(w, st, "run "+identity.Name+" setting set --name hub.remote_access --value true to allow sessions into this node")
	}
	if h.InRange != nil && !*h.InRange {
		if deref(h.Behind) == "hub" {
			writeHint(w, st, "the hub is older than this wdx: ask its admin to update it")
		} else {
			writeHint(w, st, "this wdx is older than the hub serves: run "+identity.Name+" update")
		}
	}
}

func newHubNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "See and remove the nodes of your account on the hub",
		Long:  "Every machine logged in to the account is one of its nodes.",
		Args:  clikit.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHubNodeGetCmd(), newHubNodeListCmd(), newHubNodeRemoveCmd())
	return cmd
}

// The errors of every command that asks the hub through the daemon.
var hubNodeErrors = []api.ErrorCode{api.ErrorCodeHubUnreachable, api.ErrorCodeHubVersionUnsupported, api.ErrorCodeNotLoggedIn, api.ErrorCodeDaemonUnavailable}

func newHubNodeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the nodes of your account, and which are online",
		Example: clikit.Examples(
			"Every node of the account", identity.Name+" hub node list",
			"As JSON", identity.Name+" hub node list --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The nodes by name, this one marked.\n" + `With --json: {"nodes": [...]}`,
			clikit.Errors:     clikit.CodeList(hubNodeErrors...),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).HubNodes(cmd.Context())
			if err != nil {
				return notLoggedInHint(err)
			}
			return clikit.PrintResult(cmd, hubNodeList(r))
		},
	}
}

type hubNodeList api.HubNodeList

func (l hubNodeList) Text(w io.Writer, st style.Style) {
	var t style.Table
	t.Row(st.Heading("NAME"), st.Heading("ONLINE"), st.Heading("LAST SEEN"), st.Heading("VERSION"), st.Heading("OS"), st.Heading("ID"))
	for _, n := range l.Nodes {
		name := st.Name(n.Name)
		if n.This {
			name += " " + st.Muted("(this)")
		}
		online := st.Muted("no")
		if n.Online {
			online = st.Success("yes")
		}
		t.Row(name, online, lastSeen(n.LastSeenAt), n.Version, n.Os, st.Muted(n.Id))
	}
	t.Write(w)
}

// lastSeen is when a node was last seen, "never" for one never connected.
func lastSeen(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Local().Format(time.DateTime)
}

// notLoggedInHint adds what to do to a node command run with no login.
func notLoggedInHint(err error) error {
	switch problemCode(err) {
	case api.ErrorCodeNotLoggedIn:
		return &clikit.Hinted{Err: err, Hint: "run " + identity.Name + " hub status to see the login"}
	case api.ErrorCodeHubVersionUnsupported:
		return &clikit.Hinted{Err: err, Hint: "run " + identity.Name + " hub status to see which side to update"}
	}
	return err
}

// nodeRef is how a node command names a node: --id or --name.
type nodeRef struct{ id, name string }

func (r *nodeRef) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.id, "id", "", "`id` of the node")
	cmd.Flags().StringVar(&r.name, "name", "", "`name` of the node (case-insensitive)")
	cmd.MarkFlagsOneRequired("id", "name")
	cmd.MarkFlagsMutuallyExclusive("id", "name")
	clikit.CompleteFlag(cmd, "id", completeHubNodes("id"))
	clikit.CompleteFlag(cmd, "name", completeHubNodes("name"))
}

// ref is how the API names the node.
func (r nodeRef) ref() string {
	if r.id != "" {
		return "id:" + r.id
	}
	return r.name
}

func newHubNodeGetCmd() *cobra.Command {
	var ref nodeRef
	cmd := &cobra.Command{
		Use:   "get (--id <id> | --name <name>)",
		Short: "Show a node of your account, with its key's fingerprint",
		Example: clikit.Examples(
			"A node by name", identity.Name+" hub node get --name laptop",
			"By id, as JSON", identity.Name+" hub node get --id node-1a2b3c4d --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.OutputNote: "The node.\n" + `With --json: {"node": {...}}`,
			clikit.Errors:     clikit.CodeList(append([]api.ErrorCode{api.ErrorCodeNodeNotFound}, hubNodeErrors...)...),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := clientOf(cmd).HubNode(cmd.Context(), ref.ref())
			if err != nil {
				return notLoggedInHint(err)
			}
			return clikit.PrintResult(cmd, hubNode(r))
		},
	}
	ref.addFlags(cmd)
	return cmd
}

type hubNode api.HubNodeEnvelope

func (r hubNode) Text(w io.Writer, st style.Style) {
	n := r.Node
	this := ""
	if n.This {
		this = " " + st.Muted("(this machine)")
	}
	online := "offline"
	if n.Online {
		online = st.Success("online")
	}
	fmt.Fprintf(w, "node %s%s, %s\n", st.Name(n.Name), this, online)
	var t style.Table
	t.Row(st.Muted("id"), n.Id)
	t.Row(st.Muted("fingerprint"), n.Fingerprint)
	t.Row(st.Muted("last seen"), lastSeen(n.LastSeenAt))
	t.Row(st.Muted("version"), n.Version)
	t.Row(st.Muted("os"), n.Os)
	t.Row(st.Muted("joined"), n.CreatedAt.Local().Format(time.DateTime))
	t.Write(w)
}

const removeNodeQuestion = "remove hub node"

func newHubNodeRemoveCmd() *cobra.Command {
	var ref nodeRef
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove (--id <id> | --name <name>) [--yes]",
		Short: "Remove another node from your account, cutting it off at once",
		Long: "The hub closes its connection and renews no certificate for it. You are asked\n" +
			"first; --yes answers in advance, and with --json, or without a terminal, it is\n" +
			"required. This machine cannot remove itself: wdx hub logout does.",
		Example: clikit.Examples(
			"Remove a lost laptop", identity.Name+" hub node remove --name laptop",
			"From a script", identity.Name+" hub node remove --id node-1a2b3c4d --yes --json",
		),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			clikit.FailurePrefix: "not removed",
			clikit.OutputNote:    "The removed node, as it was.\n" + `With --json: {"result": "removed", "node": {...}}`,
			clikit.Results:       string(api.ResultRemoved),
			clikit.Errors:        clikit.CodeList(append([]api.ErrorCode{api.ErrorCodeCancelled, api.ErrorCodeNodeIsThis, api.ErrorCodeNodeNotFound}, hubNodeErrors...)...),
			clikit.JSONRequires:  clikit.Annotation("yes"),
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if yes {
				clikit.LogAnswer(cmd, removeNodeQuestion, "yes", "--yes")
			} else if err := confirmNodeRemove(cmd, ref); err != nil {
				return err
			}
			r, err := clientOf(cmd).RemoveHubNode(cmd.Context(), ref.ref())
			if err != nil {
				return nodeIsThisHint(err)
			}
			return clikit.PrintResult(cmd, hubNodeRemoved(r))
		},
	}
	ref.addFlags(cmd)
	cmd.Flags().BoolVar(&yes, "yes", false, "remove it without asking")
	return cmd
}

func nodeIsThisHint(err error) error {
	if problemCode(err) == api.ErrorCodeNodeIsThis {
		return &clikit.Hinted{Err: err, Hint: "run " + identity.Name + " hub logout, which cleans up here too"}
	}
	return notLoggedInHint(err)
}

// confirmNodeRemove asks whether to remove the node; nil means yes. This
// node is not asked about: the daemon refuses it.
func confirmNodeRemove(cmd *cobra.Command, ref nodeRef) error {
	if !clikit.CanPrompt(cmd) {
		clikit.LogUnanswered(cmd, removeNodeQuestion)
		return &clikit.UsageError{Err: fmt.Errorf("removing a node needs an answer, and none can be asked (%s): pass --yes", clikit.NoPromptReason(cmd))}
	}
	r, err := clientOf(cmd).HubNode(cmd.Context(), ref.ref())
	if err != nil {
		return notLoggedInHint(err)
	}
	if r.Node.This {
		return nil
	}
	prompt, st := cmd.ErrOrStderr(), clikit.StderrStyle(cmd)
	question := fmt.Sprintf("remove node %s from your account, cutting it off at once? %s: ", st.Name(r.Node.Name), st.Command("[y/N]"))
	answer, err := clikit.Ask(prompt, bufio.NewScanner(cmd.InOrStdin()), question, "y", "n", "")
	if err != nil {
		return err
	}
	if answer != "y" {
		clikit.LogAnswer(cmd, removeNodeQuestion, "no", "prompt")
		return &client.ProblemError{Problem: api.NewProblem(api.ErrorCodeCancelled, "you answered no")}
	}
	clikit.LogAnswer(cmd, removeNodeQuestion, "yes", "prompt")
	return nil
}

type hubNodeRemoved api.HubNodeResult

func (r hubNodeRemoved) Text(w io.Writer, st style.Style) {
	fmt.Fprintf(w, "%s node %s from the account; the hub cut it off\n", st.Success("removed"), st.Name(r.Node.Name))
}

// completionWait is how long a completion that asks the hub waits.
func completionWait(cmd *cobra.Command) time.Duration {
	return cmp.Or(envOf(cmd).Timeouts.Completion, completionTimeout)
}

// completeHubNodes completes --id or --name, as key says, with the account's
// nodes. It offers nothing when the daemon or the hub does not answer in
// time.
func completeHubNodes(key string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(cmd.Context(), completionWait(cmd))
		defer cancel()
		list, err := clientOf(cmd).HubNodes(ctx)
		if err != nil {
			logging.FromContext(cmd.Context()).Info("no hub nodes to complete", "err", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []cobra.Completion
		for _, n := range list.Nodes {
			if key == "id" {
				completions = append(completions, cobra.CompletionWithDesc(n.Id, n.Name))
			} else {
				completions = append(completions, cobra.CompletionWithDesc(n.Name, n.Id))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}
