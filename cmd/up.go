package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "up",
		Short: "Bring up a local kind cluster and connect it to ConfigHub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUp(cmd.OutOrStdout(), name)
		},
	}
	c.Flags().StringVar(&name, "name", "lk", "kind cluster name")
	return c
}

func runUp(out io.Writer, name string) error {
	fmt.Fprintf(out, "lk up: would create kind cluster %q (not implemented yet)\n", name)
	return nil
}
