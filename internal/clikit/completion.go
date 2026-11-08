package clikit

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"weak"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/srjn45/wdx/internal/settings"
	"github.com/srjn45/wdx/internal/style"
)

// isCompletion reports whether args are a shell asking for completions.
func isCompletion(args []string) bool {
	return len(args) > 0 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd)
}

// flagCompletions holds what each flag of the trees built completes with, until a
// run that completes gives it to cobra (registerCompletions). cobra keeps every flag
// it is given for the life of the process, which in a process that runs many
// commands, as the tests do, keeps every tree built. A flag's entry goes with it.
var flagCompletions sync.Map // weak.Pointer[pflag.Flag] -> cobra.CompletionFunc

// CompleteFlag says what the flag of cmd completes with.
func CompleteFlag(cmd *cobra.Command, name string, complete cobra.CompletionFunc) {
	flag := cmd.Flag(name)
	if flag == nil {
		panic(fmt.Sprintf("%s has no flag --%s to complete", cmd.CommandPath(), name))
	}
	key := weak.Make(flag)
	flagCompletions.Store(key, complete)
	runtime.AddCleanup(flag, func(key weak.Pointer[pflag.Flag]) { flagCompletions.Delete(key) }, key)
}

// FlagCompletion is what the flag of cmd, its own or inherited, completes
// with, as CompleteFlag said.
func FlagCompletion(cmd *cobra.Command, name string) (cobra.CompletionFunc, bool) {
	flag := cmd.Flag(name)
	if flag == nil {
		return nil, false
	}
	complete, ok := flagCompletions.Load(weak.Make(flag))
	if !ok {
		return nil, false
	}
	return complete.(cobra.CompletionFunc), true
}

// registerCompletions gives cobra what every flag of the tree completes with.
func registerCompletions(cmd *cobra.Command) {
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if complete, ok := FlagCompletion(cmd, f.Name); ok {
			_ = cmd.RegisterFlagCompletionFunc(f.Name, complete)
		}
	})
	for _, sub := range cmd.Commands() {
		registerCompletions(sub)
	}
}

// CompleteDirs completes a flag with directories.
func CompleteDirs(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

// CompleteFiles completes a flag with the shell's own file completion.
func CompleteFiles(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveDefault
}

// completeColor completes --color with its modes.
func completeColor(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var modes []cobra.Completion
	for _, m := range style.ColorModes {
		modes = append(modes, string(m))
	}
	return modes, cobra.ShellCompDirectiveNoFileComp
}

// CompleteSettings completes --name with the settings of list, those of the
// config file only when fileOnly: the table lists them, so nothing is asked.
func CompleteSettings(list []settings.Setting, fileOnly bool) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var completions []cobra.Completion
		for _, s := range list {
			if !fileOnly || s.EnvVar == "" {
				completions = append(completions, cobra.CompletionWithDesc(s.Key, s.Description))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeHelp completes <program> help with the subcommands of the command
// named so far, and at the root with topics too, in place of a command of the
// same name.
func completeHelp(topics []Topic) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		found, rest, err := cmd.Root().Find(args)
		if err != nil || len(rest) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []cobra.Completion
		for _, sub := range found.Commands() {
			// help settings shows the topic, which names the command.
			if _, topic := findTopic(topics, sub.Name()); topic && !found.HasParent() {
				continue
			}
			if sub.IsAvailableCommand() {
				completions = append(completions, cobra.CompletionWithDesc(sub.Name(), sub.Short))
			}
		}
		if !found.HasParent() {
			for _, t := range topics {
				completions = append(completions, cobra.CompletionWithDesc(t.Name, t.Summary))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
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

// shell is a shell a program completes in.
type shell struct {
	name, load string
	generate   func(root *cobra.Command, w io.Writer) error
}

// shells are the shells the program name completes in.
func shells(name string) []shell {
	return []shell{
		{
			"bash",
			"Load it in this shell with source <(" + name + " completion bash), or for every\n" +
				"shell save it as ~/.local/share/bash-completion/completions/" + name + "; the\n" +
				"bash-completion package must be installed. The installer does the latter.",
			func(root *cobra.Command, w io.Writer) error { return root.GenBashCompletionV2(w, true) },
		},
		{
			"zsh",
			"Save it as _" + name + " in a directory of your fpath, such as\n" +
				"~/.zsh/completions with fpath=(~/.zsh/completions $fpath) before compinit\n" +
				"in ~/.zshrc. The installer does it.",
			func(root *cobra.Command, w io.Writer) error { return root.GenZshCompletion(w) },
		},
		{
			"fish",
			"Save it as ~/.config/fish/completions/" + name + ".fish. The installer does it.",
			func(root *cobra.Command, w io.Writer) error { return root.GenFishCompletion(w, true) },
		},
	}
}

// NewCompletionCmd returns the completion command of the program name, with
// a command for each shell that prints its script.
func NewCompletionCmd(name string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion",
		Short: "Print the script that completes " + name + " commands in a shell",
		Long: "The script asks " + name + " for the candidates on every Tab, so it completes\n" +
			"new commands and flags as soon as " + name + " is updated.",
		Args: NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	for _, sh := range shells(name) {
		cmd.AddCommand(&cobra.Command{
			Use:     sh.name,
			Short:   "Print the " + sh.name + " completion script",
			Long:    sh.load,
			Example: Examples("Print the script", name+" completion "+sh.name),
			Args:    cobra.NoArgs,
			Annotations: map[string]string{
				OutputNote: "The script, and nothing else.\n" + `With --json: {"shell": "` + sh.name + `", "script": "..."}`,
			},
			RunE: func(cmd *cobra.Command, _ []string) error {
				var script bytes.Buffer
				if err := sh.generate(cmd.Root(), &script); err != nil {
					return err
				}
				return PrintResult(cmd, completionScript{Shell: sh.name, Script: script.String()})
			},
		})
	}
	return cmd
}

type completionScript struct {
	Shell  string `json:"shell"`
	Script string `json:"script"`
}

func (s completionScript) Text(w io.Writer, _ style.Style) {
	fmt.Fprint(w, s.Script)
}

// completionResponse is what a shell asking for completions was given: the
// lines cobra printed, the candidates and then the directive.
func completionResponse(printed string) []string {
	return slices.DeleteFunc(strings.Split(printed, "\n"), func(line string) bool { return line == "" })
}
