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
	var (
		name  string
		force bool
	)
	c := &cobra.Command{
		Use:   "down",
		Short: "Tear down a kind cluster and its ConfigHub space",
		Long: `Tears down an lk-managed cluster.

Looks up the cluster's Space in the current cub context (via the
ijn.me/cub-lk-cluster-name annotation). If found, deletes the kind
cluster, then deletes the Space recursively (cascading Unit, Target,
Worker), then removes the local kubeconfig.

If no matching Space is found in the current context, fails with a
"are you in the right context?" hint. Pass --force to delete the local
kind cluster + kubeconfig anyway, leaving any ConfigHub side untouched.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			return runDown(ctx, cmd.OutOrStdout(), name, force)
		},
	}
	c.Flags().StringVar(&name, "name", "", "cluster name (required)")
	c.Flags().BoolVar(&force, "force", false, "delete the local kind cluster + kubeconfig even if no matching ConfigHub Space is found in the current context")
	return c
}

func runDown(ctx context.Context, out io.Writer, name string, force bool) error {
	hostname, _ := os.Hostname()
	kubeconfigPath := state.KubeconfigPathFor(name)

	client, err := cubclient.New()
	if err != nil {
		return err
	}

	spaces, err := client.ListLkSpacesForHost(ctx, hostname)
	if err != nil {
		return fmt.Errorf("look up lk spaces: %w", err)
	}
	var match *cubclient.LkSpace
	for i := range spaces {
		if spaces[i].ClusterName == name {
			match = &spaces[i]
			break
		}
	}

	if match == nil {
		if !force {
			return fmt.Errorf("no lk-managed Space for %q in the current cub context (are you in the right context?). Pass --force to delete the local kind cluster + kubeconfig anyway", name)
		}
		fmt.Fprintf(out, "No matching Space in current context — proceeding with --force (local cleanup only)\n")
	}

	// Delete the kind cluster first so the worker pod stops and the
	// connection drops; cub rejects deletion of a Connected worker.
	fmt.Fprintf(out, "Deleting kind cluster %q...\n", name)
	if err := kindcli.Delete(ctx, name, kubeconfigPath, out); err != nil {
		fmt.Fprintf(out, "  warning: %v\n", err)
	}

	if match != nil {
		fmt.Fprintf(out, "Deleting Space %q (recursive: cascades to Unit, Target, Worker)...\n", match.SpaceSlug)
		if err := retryDelete(ctx, 30*time.Second, func() error {
			return client.DeleteSpaceBySlug(ctx, match.SpaceSlug, true)
		}); err != nil {
			fmt.Fprintf(out, "  warning: %v\n", err)
		}
	}

	if err := os.Remove(kubeconfigPath); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(out, "  warning: removing kubeconfig: %v\n", err)
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
