package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/config"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/style"
)

// completionTimeout bounds how long a tab completion waits for the daemon:
// one that is slow offers nothing rather than hang the shell.
const completionTimeout = 500 * time.Millisecond

// isCompletion reports whether args are a shell asking for completions.
func isCompletion(args []string) bool {
	return len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd)
}

// completeProjects completes --id or --name, as key says, with the projects
// in state, every project when state is empty. It offers nothing when the
// daemon does not answer.
func completeProjects(key string, state api.ProjectState) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
		defer cancel()
		list, err := clientOf(cmd).ListProjects(ctx, state)
		if err != nil {
			logging.FromContext(cmd.Context()).Info("no projects to complete", "err", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []cobra.Completion
		for _, p := range list.Projects {
			if key == "id" {
				completions = append(completions, cobra.CompletionWithDesc(p.Id, p.Name))
			} else {
				completions = append(completions, cobra.CompletionWithDesc(p.Name, fmt.Sprintf("%s, %s", p.State, p.Id)))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeDirs completes a flag with directories.
func completeDirs(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

// completeColor completes --color with its modes.
func completeColor(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var modes []cobra.Completion
	for _, m := range config.ColorModes {
		modes = append(modes, string(m))
	}
	return modes, cobra.ShellCompDirectiveNoFileComp
}

// completeHelp completes wdx help with the subcommands of the command named
// so far, and at the root with the topics too.
func completeHelp(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	found, rest, err := cmd.Root().Find(args)
	if err != nil || len(rest) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var completions []cobra.Completion
	for _, sub := range found.Commands() {
		if sub.IsAvailableCommand() {
			completions = append(completions, cobra.CompletionWithDesc(sub.Name(), sub.Short))
		}
	}
	if !found.HasParent() {
		for _, t := range topics {
			completions = append(completions, cobra.CompletionWithDesc(t.name, t.summary))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// setCompletions makes every command in the tree offer no files where it
// completes nothing else: no command takes a file as an argument.
func setCompletions(cmd *cobra.Command) {
	if cmd.ValidArgsFunction == nil {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}
	for _, sub := range cmd.Commands() {
		setCompletions(sub)
	}
}

// shells are the shells wdx completes in.
var shells = []struct {
	name, load string
	generate   func(root *cobra.Command, w io.Writer) error
}{
	{
		"bash",
		"Load it in this shell with source <(" + identity.Name + " completion bash), or for every\n" +
			"shell save it as ~/.local/share/bash-completion/completions/" + identity.Name + "; the\n" +
			"bash-completion package must be installed. The installer does the latter.",
		func(root *cobra.Command, w io.Writer) error { return root.GenBashCompletionV2(w, true) },
	},
	{
		"zsh",
		"Save it as _" + identity.Name + " in a directory of your fpath, such as\n" +
			"~/.zsh/completions with fpath=(~/.zsh/completions $fpath) before compinit\n" +
			"in ~/.zshrc. The installer does it.",
		func(root *cobra.Command, w io.Writer) error { return root.GenZshCompletion(w) },
	},
	{
		"fish",
		"Save it as ~/.config/fish/completions/" + identity.Name + ".fish. The installer does it.",
		func(root *cobra.Command, w io.Writer) error { return root.GenFishCompletion(w, true) },
	},
}

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Print the script that completes " + identity.Name + " commands in a shell",
		Long: "The script asks " + identity.Name + " for the candidates on every Tab, so it completes\n" +
			"new commands and flags as soon as " + identity.Name + " is updated. Project names\n" +
			"and ids come from the daemon; nothing is offered when it is not running.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	for _, sh := range shells {
		cmd.AddCommand(&cobra.Command{
			Use:     sh.name,
			Short:   "Print the " + sh.name + " completion script",
			Long:    sh.load,
			Example: examples("Print the script", identity.Name+" completion "+sh.name),
			Args:    cobra.NoArgs,
			Annotations: map[string]string{
				outputNote: "The script, and nothing else.\n" + `With --json: {"shell": "` + sh.name + `", "script": "..."}`,
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				var script bytes.Buffer
				if err := sh.generate(cmd.Root(), &script); err != nil {
					return err
				}
				return printResult(cmd, completionScript{Shell: sh.name, Script: script.String()})
			},
		})
	}
	return cmd
}

type completionScript struct {
	Shell  string `json:"shell"`
	Script string `json:"script"`
}

func (s completionScript) text(w io.Writer, _ style.Style) {
	fmt.Fprint(w, s.Script)
}

// completionResponse is what a shell asking for completions was given: the
// lines cobra printed, the candidates and then the directive.
func completionResponse(printed string) []string {
	return slices.DeleteFunc(strings.Split(printed, "\n"), func(line string) bool { return line == "" })
}
