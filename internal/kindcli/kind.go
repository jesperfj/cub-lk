// Package kindcli wraps the `kind` CLI for cluster lifecycle. We shell out
// because kind has no Go library API designed for external consumers.
package kindcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// Create runs `kind create cluster --name <name>`. stdout/stderr stream to
// the provided writer so the user sees kind's progress. Returns the kube
// context name kind wires up (`kind-<name>`).
func Create(ctx context.Context, name string, out io.Writer) (string, error) {
	cmd := exec.CommandContext(ctx, "kind", "create", "cluster", "--name", name)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kind create cluster: %w", err)
	}
	return "kind-" + name, nil
}

// Delete runs `kind delete cluster --name <name>`.
func Delete(ctx context.Context, name string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", name)
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

// KubectlApply pipes the manifest to `kubectl --context <ctx> apply -f -`.
func KubectlApply(ctx context.Context, kubeContext string, manifest []byte, out io.Writer) error {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl not found on PATH")
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "apply", "-f", "-")
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
		"-t", "Kubernetes",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cub worker install --export: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
