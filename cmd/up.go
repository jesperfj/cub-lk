package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/jesperfj/cub-lk/internal/cubclient"
	"github.com/jesperfj/cub-lk/internal/kindcli"
	"github.com/jesperfj/cub-lk/internal/state"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var (
		name      string
		spaceSlug string
		namespace string
		skipUnit  bool
	)
	c := &cobra.Command{
		Use:   "up",
		Short: "Bring up a local kind cluster and connect it to ConfigHub",
		Long: `Creates a kind cluster and a ConfigHub Space (default name "<name>-cluster")
containing a worker, target, and (by default) a worker-config Unit. Applies
the worker manifest to the cluster.

The cluster's kubeconfig is written to a per-cluster file under
$CUB_CONFIG/lk/<name>.kubeconfig (not merged into ~/.kube/config).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()
			return runUp(ctx, cmd.OutOrStdout(), upOptions{
				name:      name,
				spaceSlug: spaceSlug,
				namespace: namespace,
				skipUnit:  skipUnit,
			})
		},
	}
	c.Flags().StringVar(&name, "name", "", "cluster name (auto-generated if empty)")
	c.Flags().StringVar(&spaceSlug, "space", "", "ConfigHub space slug (defaults to <name>-cluster)")
	c.Flags().StringVar(&namespace, "namespace", "confighub", "Kubernetes namespace for the worker target binding")
	c.Flags().BoolVar(&skipUnit, "no-unit", false, "skip creating the worker-config Unit in ConfigHub")
	return c
}

type upOptions struct {
	name      string
	spaceSlug string
	namespace string
	skipUnit  bool
}

func runUp(ctx context.Context, out io.Writer, opts upOptions) error {
	if err := kindcli.EnsureAvailable(); err != nil {
		return err
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found on PATH")
	}
	if _, err := exec.LookPath("cub"); err != nil {
		return fmt.Errorf("cub not found on PATH")
	}

	client, err := cubclient.New()
	if err != nil {
		return err
	}

	if opts.name == "" {
		fmt.Fprintln(out, "Generating cluster name...")
		opts.name, err = client.NewPrefix(ctx)
		if err != nil {
			return err
		}
	}
	if opts.spaceSlug == "" {
		opts.spaceSlug = opts.name + "-cluster"
	}

	st, err := state.Load()
	if err != nil {
		return err
	}
	if _, ok := st.Get(opts.name); ok {
		return fmt.Errorf("cluster %q is already tracked in state at %s", opts.name, state.Path())
	}

	if exists, err := client.SpaceExists(ctx, opts.spaceSlug); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("ConfigHub space %q already exists", opts.spaceSlug)
	}

	if exists, err := kindcli.Exists(ctx, opts.name); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("kind cluster %q already exists", opts.name)
	}

	const workerSlug = "worker"
	const targetSlug = "target"
	const unitSlug = "worker-config"
	kubeconfigPath := state.KubeconfigPathFor(opts.name)

	fmt.Fprintf(out, "Creating kind cluster %q (kubeconfig: %s)...\n", opts.name, kubeconfigPath)
	kubeContext, err := kindcli.Create(ctx, opts.name, kubeconfigPath, out)
	if err != nil {
		return err
	}
	rollback := []func(){
		func() {
			fmt.Fprintf(out, "Rolling back: kind delete cluster %q\n", opts.name)
			_ = kindcli.Delete(context.Background(), opts.name, kubeconfigPath, out)
		},
	}
	commit := false
	defer func() {
		if commit {
			return
		}
		for i := len(rollback) - 1; i >= 0; i-- {
			rollback[i]()
		}
	}()

	fmt.Fprintf(out, "Creating ConfigHub space %q...\n", opts.spaceSlug)
	spaceID, err := client.CreateSpace(ctx, opts.spaceSlug, opts.spaceSlug, map[string]string{"managed-by": "lk"})
	if err != nil {
		return err
	}
	_ = spaceID // kept for future per-entity cleanup hooks
	rollback = append(rollback, func() {
		fmt.Fprintf(out, "Rolling back: cub space delete --recursive %q\n", opts.spaceSlug)
		_ = client.DeleteSpaceBySlug(context.Background(), opts.spaceSlug, true)
	})

	fmt.Fprintf(out, "Creating ConfigHub worker %q (Kubernetes provider)...\n", workerSlug)
	workerID, err := client.CreateBridgeWorker(ctx, spaceID, workerSlug, workerSlug,
		[][2]string{{"Kubernetes", "Kubernetes/YAML"}})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Generating worker manifest (cub worker install --export --include-secret)...\n")
	manifest, err := kindcli.CubWorkerInstallExport(ctx, workerSlug, opts.spaceSlug)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Creating ConfigHub target %q bound to worker %q...\n", targetSlug, workerSlug)
	targetID, err := client.CreateKubernetesTarget(ctx, spaceID, workerID, targetSlug, targetSlug, kubeContext, opts.namespace)
	if err != nil {
		return err
	}

	if !opts.skipUnit {
		fmt.Fprintf(out, "Storing worker manifest as Unit %q...\n", unitSlug)
		if _, err := client.CreateYAMLUnit(ctx, spaceID, targetID, unitSlug, unitSlug, manifest); err != nil {
			return err
		}
	}

	fmt.Fprintf(out, "Applying worker manifest to cluster (kubectl --kubeconfig %s apply)...\n", kubeconfigPath)
	if err := kindcli.KubectlApply(ctx, kubeconfigPath, manifest, out); err != nil {
		return err
	}

	rec := state.Cluster{
		Name:           opts.name,
		KubeContext:    kubeContext,
		KubeconfigPath: kubeconfigPath,
		SpaceSlug:      opts.spaceSlug,
		WorkerSlug:     workerSlug,
		TargetSlug:     targetSlug,
		CreatedAt:      time.Now().UTC(),
	}
	if !opts.skipUnit {
		rec.UnitSlug = unitSlug
	}
	if err := st.Add(rec); err != nil {
		return err
	}
	if err := st.Save(); err != nil {
		return err
	}

	commit = true
	fmt.Fprintf(out, "\nDone.\n  cluster:    %s\n  kubeconfig: %s\n  context:    %s\n  space:      %s\n  worker:     %s/%s\n  target:     %s/%s\n\nUse the cluster:\n  KUBECONFIG=%s kubectl get pods -A\n",
		opts.name, kubeconfigPath, kubeContext, opts.spaceSlug, opts.spaceSlug, workerSlug, opts.spaceSlug, targetSlug, kubeconfigPath)
	return nil
}

