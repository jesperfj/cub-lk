package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
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

	// Kill the cluster first so the worker pod stops and the connection
	// drops — cub rejects deletion of a Connected worker.
	fmt.Fprintf(out, "Deleting kind cluster %q...\n", rec.Name)
	if err := kindcli.Delete(ctx, rec.Name, rec.KubeconfigPath, out); err != nil {
		fmt.Fprintf(out, "  warning: %v\n", err)
	}

	fmt.Fprintf(out, "Deleting Space %q (recursive: cascades to Unit, Target, Worker)...\n", rec.SpaceSlug)
	if err := retryDelete(ctx, 30*time.Second, func() error {
		return client.DeleteSpaceBySlug(ctx, rec.SpaceSlug, true)
	}); err != nil {
		fmt.Fprintf(out, "  warning: %v\n", err)
	}

	if rec.KubeconfigPath != "" {
		if err := os.Remove(rec.KubeconfigPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(out, "  warning: removing kubeconfig: %v\n", err)
		}
	}

	st.Remove(rec.Name)
	if err := st.Save(); err != nil {
		return err
	}

	fmt.Fprintf(out, "\nDone.\n")
	return nil
}

// retryDelete retries fn with 2s backoff until it succeeds or the budget
// expires. Used while waiting for the worker connection to drop after the
// kind cluster goes away.
func retryDelete(ctx context.Context, budget time.Duration, fn func() error) error {
	deadline := time.Now().Add(budget)
	var lastErr error
	for {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
