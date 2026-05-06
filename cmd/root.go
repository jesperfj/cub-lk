package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "lk",
		Short: "Local Kubernetes plugin for cub",
		Long: `lk is a cub plugin that brings up a local kind cluster and wires it
into ConfigHub as a worker + target with a single command.`,
		SilenceUsage: true,
	}
	root.AddCommand(newUpCmd(), newDownCmd(), newListCmd(), newVersionCmd())
	return root
}

func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
