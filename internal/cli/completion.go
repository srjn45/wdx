package cli

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/srjn45/wdx/internal/api"
	"github.com/srjn45/wdx/internal/clikit"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// completionTimeout bounds how long a tab completion waits for the daemon:
// one that is slow offers nothing rather than hang the shell.
const completionTimeout = 500 * time.Millisecond

// completeProjects completes --id or --name, as key says, with the projects
// in state, every project when state is empty. It offers nothing when the
// daemon does not answer.
func completeProjects(key string, state api.ProjectState) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(envOf(cmd).Timeouts.Completion, completionTimeout))
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

// completeTerminals completes --id or --name, as key says, with the
// terminals of the project the command line names, else of the one the
// working directory is in. It offers nothing when the daemon does not
// answer.
func completeTerminals(key string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(envOf(cmd).Timeouts.Completion, completionTimeout))
		defer cancel()
		var scope projectScope
		scope.id, _ = cmd.Flags().GetString("project-id")
		scope.name, _ = cmd.Flags().GetString("project-name")
		var dir string
		if !scope.given() {
			dir = envOf(cmd).WorkDir
		}
		list, err := clientOf(cmd).ListTerminals(ctx, scope.ref(), dir)
		if err != nil {
			logging.FromContext(cmd.Context()).Info("no terminals to complete", "err", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []cobra.Completion
		for _, t := range list.Terminals {
			if key == "id" {
				completions = append(completions, cobra.CompletionWithDesc(t.Id, fmt.Sprintf("%s in %s", t.Name, t.ProjectName)))
			} else {
				completions = append(completions, cobra.CompletionWithDesc(t.Name, fmt.Sprintf("%s, %s", t.State, t.Id)))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeAgents completes --id or --name, as key says, with the agents of
// the project the command line names, else of the one the working directory
// is in. It offers nothing when the daemon does not answer.
func completeAgents(key string) cobra.CompletionFunc {
	return completeAgentsWhere(key, func(api.Agent) bool { return true })
}

// completeParents is completeAgents for an agent to create another under,
// which a removed one cannot be.
func completeParents(key string) cobra.CompletionFunc {
	return completeAgentsWhere(key, func(a api.Agent) bool { return a.State != api.AgentStateRemoved })
}

func completeAgentsWhere(key string, keep func(api.Agent) bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(envOf(cmd).Timeouts.Completion, completionTimeout))
		defer cancel()
		var scope projectScope
		scope.id, _ = cmd.Flags().GetString("project-id")
		scope.name, _ = cmd.Flags().GetString("project-name")
		var dir string
		if !scope.given() {
			dir = envOf(cmd).WorkDir
		}
		list, err := clientOf(cmd).ListAgents(ctx, scope.ref(), dir)
		if err != nil {
			logging.FromContext(cmd.Context()).Info("no agents to complete", "err", err)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []cobra.Completion
		for _, a := range api.FlattenAgents(list.Agents) {
			if !keep(a) {
				continue
			}
			if key == "id" {
				completions = append(completions, cobra.CompletionWithDesc(a.Id, fmt.Sprintf("%s in %s", a.Name, a.ProjectName)))
			} else {
				completions = append(completions, cobra.CompletionWithDesc(a.Name, fmt.Sprintf("%s, %s", a.State, a.Id)))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

func newCompletionCmd() *cobra.Command {
	cmd := clikit.NewCompletionCmd(identity.Name)
	cmd.Long = "The script asks " + identity.Name + " for the candidates on every Tab, so it completes\n" +
		"new commands and flags as soon as " + identity.Name + " is updated. Project and\n" +
		"terminal names and ids come from the daemon; nothing is offered when it is\n" +
		"not running."
	return cmd
}

// completeACAs offers the AI coding agents new agents can run, as the daemon says.
func completeACAs(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(envOf(cmd).Timeouts.Completion, completionTimeout))
	defer cancel()
	list, err := clientOf(cmd).ListAcas(ctx)
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no acas to complete", "err", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	completions := make([]cobra.Completion, 0, len(list.Acas))
	for _, a := range list.Acas {
		completions = append(completions, a.Id)
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// completeModels offers the models of the AI coding agent --aca names, or of the
// default, each with its name.
func completeModels(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), cmp.Or(envOf(cmd).Timeouts.Completion, completionTimeout))
	defer cancel()
	acaID, _ := cmd.Flags().GetString("aca")
	id, err := chosenACA(ctx, clientOf(cmd), acaID)
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no models to complete", "err", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	list, err := clientOf(cmd).ListAcaModels(ctx, id)
	if err != nil {
		logging.FromContext(cmd.Context()).Info("no models to complete", "err", err)
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	completions := make([]cobra.Completion, 0, len(list.Models))
	for _, m := range list.Models {
		completions = append(completions, cobra.CompletionWithDesc(m.Id, m.Name))
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}
