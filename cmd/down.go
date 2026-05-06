package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "down",
		Short: "Delete a local kind cluster managed by lk",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDown(cmd.OutOrStdout(), name)
		},
	}
	c.Flags().StringVar(&name, "name", "lk", "kind cluster name")
	return c
}

func runDown(out io.Writer, name string) error {
	fmt.Fprintf(out, "lk down: would delete kind cluster %q (not implemented yet)\n", name)
	return nil
}
