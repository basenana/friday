package main

import (
	"fmt"
	"os"

	"github.com/basenana/friday/sandbox"
	"github.com/spf13/cobra"
)

var sandboxCmd = &cobra.Command{Use: "sandbox", Short: "Manage sandbox command permissions"}

// sandboxAllowCmd persists a project-level allow grant for a command. It is
// the headless counterpart to the interactive approval form: the grant lands
// in <DataDir>/projects/<projectID>/sandbox.json and applies to every future
// session opened from this project directory.
var sandboxAllowCmd = &cobra.Command{
	Use:   "allow <command>",
	Short: "Persist a project-level sandbox command grant",
	Long: `Persist a project-level sandbox command grant.

The grant is stored in sandbox.json under the Friday data directory keyed by
the current project, not inside the repository, and is merged into the allow
list of every future session opened from this directory. Commands matched by
deny rules (for example sudo) can never be granted.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		command := args[0]
		cfgSandbox := cfg.Sandbox
		if cfgSandbox == nil {
			cfgSandbox = sandbox.DefaultConfig()
		}
		if err := sandbox.ValidateGrantableCommand(cfgSandbox, command); err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		path, err := sandbox.ProjectAllowPath(cfg.DataDirPath(), cwd)
		if err != nil {
			return err
		}
		if err := sandbox.AppendProjectAllow(path, command); err != nil {
			return err
		}
		fmt.Printf("Allowed %q for this project (%s)\n", command, path)
		return nil
	},
}

func init() {
	sandboxCmd.AddCommand(sandboxAllowCmd)
	rootCmd.AddCommand(sandboxCmd)
}
