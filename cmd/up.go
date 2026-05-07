package cmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jesperfj/cub-lk/internal/cubclient"
	"github.com/jesperfj/cub-lk/internal/docker"
	"github.com/jesperfj/cub-lk/internal/kindcli"
	"github.com/jesperfj/cub-lk/internal/state"
	"github.com/spf13/cobra"
)

const (
	// Annotation keys recorded on every lk-created Space. The "ijn.me/"
	// prefix is the author's personal domain. Annotations are visible via
	// `cub space get -o yaml` and the UI but are not currently usable in
	// `--where` filters (the where-filter parser doesn't accept dotted or
	// hyphenated label/annotation keys). For "show me all lk spaces" use
	// the labelLk Label below.
	annotationClusterName = "ijn.me/cub-lk-cluster-name"
	annotationPortRange   = "ijn.me/cub-lk-port-range"
	annotationHost        = "ijn.me/cub-lk-host"

	// labelLk is a marker label set on every lk Space so it is queryable:
	// `cub space list --where "Labels.cub-lk = 'true'"`. Matches the
	// project name; hyphens are accepted by the where-filter parser as
	// long as the key is unquoted.
	labelLk = "cub-lk"

	// Default search range and window size for port allocation.
	portRangeStart = 30000
	portRangeEnd   = 30099
	portRangeSize  = 10
)

func newUpCmd() *cobra.Command {
	var (
		name      string
		spaceSlug string
		namespace string
		skipUnit  bool
		mountArgs []string
		noPorts   bool
	)
	c := &cobra.Command{
		Use:   "up",
		Short: "Bring up a local kind cluster and connect it to ConfigHub",
		Long: `Creates a kind cluster and a ConfigHub Space (default name "<name>-cluster")
containing a worker, target, and (by default) a worker-config Unit. Applies
the worker manifest to the cluster.

The cluster's kubeconfig is written to a per-cluster file under
$CUB_CONFIG/lk/<name>.kubeconfig (not merged into ~/.kube/config).

Use --mount HOST[:CONTAINER] (repeatable) to bind-mount host directories
into the cluster node. Pods can then reach them via hostPath volumes on
the container path. CONTAINER defaults to /mnt/<basename of host>.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()
			mounts, err := parseMounts(mountArgs)
			if err != nil {
				return err
			}
			return runUp(ctx, cmd.OutOrStdout(), upOptions{
				name:      name,
				spaceSlug: spaceSlug,
				namespace: namespace,
				skipUnit:  skipUnit,
				mounts:    mounts,
				noPorts:   noPorts,
			})
		},
	}
	c.Flags().StringVar(&name, "name", "", "cluster name (auto-generated if empty)")
	c.Flags().StringVar(&spaceSlug, "space", "", "ConfigHub space slug (defaults to <name>-cluster)")
	c.Flags().StringVar(&namespace, "namespace", "confighub", "Kubernetes namespace for the worker target binding")
	c.Flags().BoolVar(&skipUnit, "no-unit", false, "skip creating the worker-config Unit in ConfigHub")
	c.Flags().StringArrayVar(&mountArgs, "mount", nil, "host:container bind mount (repeatable; container path defaults to /mnt/<basename>)")
	c.Flags().BoolVar(&noPorts, "no-ports", false, "skip default localhost:30000-30009 port mappings (useful when running multiple lk clusters)")
	return c
}

type upOptions struct {
	name      string
	spaceSlug string
	namespace string
	skipUnit  bool
	mounts    []kindcli.Mount
	noPorts   bool
}

// dockerInternalURLFor returns a rewritten URL with the loopback host
// replaced by "host.docker.internal" so a pod inside a docker container
// (e.g. kind's control-plane node) can reach a server running on the host.
// Returns "" when no rewrite is needed.
//
// Recognized loopback hosts: localhost, 127.0.0.1, 0.0.0.0, [::1].
// macOS/Windows Docker Desktop provides host.docker.internal automatically;
// on Linux it requires `--add-host=host.docker.internal:host-gateway` on
// the kind container, which we don't set up yet.
func dockerInternalURLFor(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1":
		port := u.Port()
		newHost := "host.docker.internal"
		if port != "" {
			u.Host = newHost + ":" + port
		} else {
			u.Host = newHost
		}
		return u.String()
	}
	return ""
}

// parseMounts parses --mount values of the form HOST[:CONTAINER]. The host
// path is tilde- and relative-expanded to absolute and validated to exist;
// container defaults to /mnt/<basename>.
func parseMounts(args []string) ([]kindcli.Mount, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := make([]kindcli.Mount, 0, len(args))
	for _, raw := range args {
		host, container, _ := strings.Cut(raw, ":")
		host = strings.TrimSpace(host)
		container = strings.TrimSpace(container)
		if host == "" {
			return nil, fmt.Errorf("--mount %q: empty host path", raw)
		}
		if strings.HasPrefix(host, "~") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("--mount %q: expand ~: %w", raw, err)
			}
			host = filepath.Join(home, strings.TrimPrefix(host, "~"))
		}
		abs, err := filepath.Abs(host)
		if err != nil {
			return nil, fmt.Errorf("--mount %q: resolve %q: %w", raw, host, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("--mount %q: %w", raw, err)
		}
		if container == "" {
			container = "/mnt/" + filepath.Base(abs)
		}
		if !strings.HasPrefix(container, "/") {
			return nil, fmt.Errorf("--mount %q: container path must be absolute", raw)
		}
		out = append(out, kindcli.Mount{HostPath: abs, ContainerPath: container})
	}
	return out, nil
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

	var ports []kindcli.PortMapping
	var portRange string
	if !opts.noPorts {
		bound, err := docker.BoundHostPorts(ctx)
		if err != nil {
			return fmt.Errorf("probe docker for bound ports: %w", err)
		}
		startPort, err := docker.PickFreePortWindow(bound, portRangeStart, portRangeEnd, portRangeSize)
		if err != nil {
			return err
		}
		ports = make([]kindcli.PortMapping, 0, portRangeSize)
		for p := startPort; p < startPort+portRangeSize; p++ {
			ports = append(ports, kindcli.PortMapping{HostPort: p, ContainerPort: p})
		}
		portRange = fmt.Sprintf("%d-%d", startPort, startPort+portRangeSize-1)
	}
	fmt.Fprintf(out, "Creating kind cluster %q (kubeconfig: %s)...\n", opts.name, kubeconfigPath)
	kubeContext, err := kindcli.Create(ctx, opts.name, kubeconfigPath, ports, opts.mounts, out)
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
	hostname, _ := os.Hostname()
	annotations := map[string]string{
		annotationClusterName: opts.name,
		annotationHost:        hostname,
	}
	if portRange != "" {
		annotations[annotationPortRange] = portRange
	}
	spaceID, err := client.CreateSpace(ctx, opts.spaceSlug, opts.spaceSlug,
		map[string]string{labelLk: "true"}, annotations)
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

	overrideURL := dockerInternalURLFor(client.Server())
	if overrideURL != "" {
		fmt.Fprintf(out, "Worker pod CONFIGHUB_URL: %s (rewritten from %s so pod can reach host)\n", overrideURL, client.Server())
	}
	fmt.Fprintf(out, "Generating worker manifest (cub worker install --export --include-secret)...\n")
	manifest, err := kindcli.CubWorkerInstallExport(ctx, workerSlug, opts.spaceSlug, overrideURL)
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
		PortRange:      portRange,
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
	fmt.Fprintf(out, "\nDone.\n  cluster:    %s\n  kubeconfig: %s\n  context:    %s\n  space:      %s\n  worker:     %s/%s\n  target:     %s/%s\n",
		opts.name, kubeconfigPath, kubeContext, opts.spaceSlug, opts.spaceSlug, workerSlug, opts.spaceSlug, targetSlug)
	if len(ports) > 0 {
		fmt.Fprintf(out, "\nPort mappings (host → NodePort):\n")
		for _, p := range ports {
			fmt.Fprintf(out, "  localhost:%-5d → nodePort %d\n", p.HostPort, p.ContainerPort)
		}
	} else {
		fmt.Fprintf(out, "\nPort mappings: none (use kubectl port-forward to reach services).\n")
	}
	if len(opts.mounts) > 0 {
		fmt.Fprintf(out, "\nHost mounts (host → node, accessible to pods via hostPath):\n")
		for _, m := range opts.mounts {
			fmt.Fprintf(out, "  %s → %s\n", m.HostPath, m.ContainerPath)
		}
	}
	fmt.Fprintf(out, "\nUse the cluster:\n  KUBECONFIG=%s kubectl get pods -A\n", kubeconfigPath)
	return nil
}

