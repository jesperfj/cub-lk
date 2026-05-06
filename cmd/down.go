package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jesperfj/cub-lk/internal/cubclient"
	"github.com/jesperfj/cub-lk/internal/kindcli"
	"github.com/jesperfj/cub-lk/internal/state"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "down",
		Short: "Tear down a kind cluster and its ConfigHub space",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			return runDown(ctx, cmd.OutOrStdout(), name)
		},
	}
	c.Flags().StringVar(&name, "name", "", "cluster name (required)")
	return c
}

func runDown(ctx context.Context, out io.Writer, name string) error {
	st, err := state.Load()
	if err != nil {
		return err
	}
	rec, ok := st.Get(name)
	if !ok {
		return fmt.Errorf("cluster %q not found in lk state (%s); if you created the cluster manually, use `kind delete cluster --name %s`", name, state.Path(), name)
	}

	client, err := cubclient.New()
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Deleting ConfigHub space %q (cascades to worker, target, unit)...\n", rec.SpaceSlug)
	if err := client.DeleteSpaceBySlug(ctx, rec.SpaceSlug); err != nil {
		fmt.Fprintf(out, "  warning: %v\n", err)
	}

	fmt.Fprintf(out, "Deleting kind cluster %q...\n", rec.Name)
	if err := kindcli.Delete(ctx, rec.Name, out); err != nil {
		fmt.Fprintf(out, "  warning: %v\n", err)
	}

	st.Remove(rec.Name)
	if err := st.Save(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nDone.\n")
	return nil
}
