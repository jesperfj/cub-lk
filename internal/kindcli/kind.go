// Package kindcli wraps the `kind` CLI for cluster lifecycle. We shell out
// because kind has no Go library API designed for external consumers.
package kindcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// EnsureAvailable returns an error if kind is not on PATH.
func EnsureAvailable() error {
	if _, err := exec.LookPath("kind"); err != nil {
		return fmt.Errorf("kind not found on PATH; install from https://kind.sigs.k8s.io")
	}
	return nil
}

// PortMapping is a host→container port pair, both on the kind control-plane node.
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "TCP" or "UDP"; empty = TCP
}

// DefaultPortMappings is the set of host:container port mappings opened on
// the kind control-plane node when no override is given. The 30000-30009
// range mirrors the standard NodePort range so a Service of type NodePort
// with `nodePort: 30000` is reachable as `localhost:30000`. The 8080/8443
// pair maps the well-known dev ports to NodePort 30080/30443 for things
// like Argo (`localhost:8443` → argocd-server NodePort 30443).
func DefaultPortMappings() []PortMapping {
	mappings := []PortMapping{
		{HostPort: 8080, ContainerPort: 30080},
		{HostPort: 8443, ContainerPort: 30443},
	}
	for p := 30000; p <= 30009; p++ {
		mappings = append(mappings, PortMapping{HostPort: p, ContainerPort: p})
	}
	return mappings
}

// Create runs `kind create cluster --name <name> --kubeconfig <path>` with
// a generated kind config that opens the given port mappings on the
// control-plane node. kind writes the cluster's kubeconfig to the given
// path (rather than merging into ~/.kube/config) so each lk cluster's
// credentials stay isolated. Returns the kube context name kind wires up
// (`kind-<name>`).
func Create(ctx context.Context, name, kubeconfigPath string, ports []PortMapping, out io.Writer) (string, error) {
	configPath, err := writeKindConfig(name, ports)
	if err != nil {
		return "", err
	}
	defer os.Remove(configPath)

	cmd := exec.CommandContext(ctx, "kind", "create", "cluster",
		"--name", name,
		"--kubeconfig", kubeconfigPath,
		"--config", configPath,
	)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kind create cluster: %w", err)
	}
	return "kind-" + name, nil
}

// writeKindConfig generates a kind v1alpha4 cluster config to a temp file
// and returns its path. The caller is responsible for removing the file.
func writeKindConfig(name string, ports []PortMapping) (string, error) {
	var b strings.Builder
	b.WriteString("kind: Cluster\n")
	b.WriteString("apiVersion: kind.x-k8s.io/v1alpha4\n")
	b.WriteString("name: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("nodes:\n")
	b.WriteString("- role: control-plane\n")
	if len(ports) > 0 {
		b.WriteString("  extraPortMappings:\n")
		for _, p := range ports {
			proto := p.Protocol
			if proto == "" {
				proto = "TCP"
			}
			fmt.Fprintf(&b, "  - hostPort: %d\n    containerPort: %d\n    protocol: %s\n", p.HostPort, p.ContainerPort, proto)
		}
	}

	f, err := os.CreateTemp("", "lk-kind-config-*.yaml")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// Delete runs `kind delete cluster --name <name>`. kubeconfigPath, if set,
// scopes the credential cleanup to the dedicated file.
func Delete(ctx context.Context, name, kubeconfigPath string, out io.Writer) error {
	args := []string{"delete", "cluster", "--name", name}
	if kubeconfigPath != "" {
		args = append(args, "--kubeconfig", kubeconfigPath)
	}
	cmd := exec.CommandContext(ctx, "kind", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kind delete cluster: %w", err)
	}
	return nil
}

// Exists reports whether a kind cluster with the given name exists.
func Exists(ctx context.Context, name string) (bool, error) {
	out, err := exec.CommandContext(ctx, "kind", "get", "clusters").Output()
	if err != nil {
		return false, fmt.Errorf("kind get clusters: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// KubectlApply pipes the manifest to `kubectl --kubeconfig <path> apply -f -`.
func KubectlApply(ctx context.Context, kubeconfigPath string, manifest []byte, out io.Writer) error {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found on PATH")
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(manifest)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}

// CubWorkerInstallExport shells out to `cub worker install <worker> --space
// <space> --export -t Kubernetes` and returns the manifest YAML on stdout.
// We shell out (rather than re-implement) because the manifest generator is
// 250 lines of cub-internal logic; tracking it in lk would drift over time.
// SDK extraction candidate: lift this into cubapi as a library function.
func CubWorkerInstallExport(ctx context.Context, workerSlug, spaceSlug string) ([]byte, error) {
	if _, err := exec.LookPath("cub"); err != nil {
		return nil, fmt.Errorf("cub not found on PATH")
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "cub", "worker", "install",
		workerSlug,
		"--space", spaceSlug,
		"--export",
		"--include-secret",
		"-t", "Kubernetes",
	)
	cmd.Env = scrubbedCubEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cub worker install --export: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// scrubbedCubEnv returns the current process env minus CUB_CONFIG and
// CUB_PLUGIN. Background: when lk runs as a cub plugin, the parent cub sets
// CUB_CONFIG to the config *directory* (~/.confighub) and CUB_PLUGIN=1.
// However, cub's own NewContextManagerWithPath treats CUB_CONFIG as a *file*
// path, so any cub subprocess that inherits CUB_CONFIG=<dir> crashes with
// "read <dir>: is a directory". Scrubbing lets the child cub use its
// default config file location.
//
// Bug report worth filing against cub: the plugin developer guide
// documents CUB_CONFIG as a directory, but main.go treats it as a file.
func scrubbedCubEnv() []string {
	skip := map[string]struct{}{
		"CUB_CONFIG": {},
		"CUB_PLUGIN": {},
	}
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}
