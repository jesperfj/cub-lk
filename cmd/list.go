package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List local kind clusters managed by lk",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.OutOrStdout())
		},
	}
}

func runList(out io.Writer) error {
	fmt.Fprintln(out, "lk list: no clusters tracked yet (not implemented)")
	return nil
}
