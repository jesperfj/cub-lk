package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/jesperfj/cub-lk/internal/state"
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
	st, err := state.Load()
	if err != nil {
		return err
	}
	if len(st.Clusters) == 0 {
		fmt.Fprintln(out, "No clusters tracked.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSPACE\tKUBECONFIG\tCREATED")
	for _, c := range st.Clusters {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.SpaceSlug, c.KubeconfigPath, c.CreatedAt.Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}
