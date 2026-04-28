// Package recipe implements the "dashgen recipe" subcommand group, which
// provides developer-experience tooling for the authoring lifecycle of YAML
// recipes (init, scaffold, lint, list, show, test, explain, diff).
//
// See docs/RECIPES-CLI.md for the full specification.
// Phase 2A ships init (T2A.1) and scaffold (T2A.2).
package recipe

import "github.com/spf13/cobra"

// NewCmd returns the "dashgen recipe" cobra parent. Callers (main.newRootCmd)
// add it to the root command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipe",
		Short: "Manage and author YAML recipes",
		Long: `Developer-experience tools for creating, linting, testing, and debugging
YAML recipe files. See 'dashgen recipe <subcommand> --help' for details.`,
	}
	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newScaffoldCmd())
	cmd.AddCommand(newLintCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newTestCmd())
	return cmd
}
