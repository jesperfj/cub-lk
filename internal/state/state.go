// Package state provides path conventions and a directory-listing helper
// for the per-cluster kubeconfig files lk creates. There is no state file:
// "lk knows about this cluster" is implied by the existence of
// $CUB_CONFIG/lk/<name>.kubeconfig (created by `lk up`, removed by
// `lk down`). All other per-cluster metadata comes from the corresponding
// ConfigHub Space's annotations.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KubeconfigDir returns the directory where lk stores per-cluster kubeconfigs.
func KubeconfigDir() string {
	dir := os.Getenv("CUB_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".confighub")
	}
	return filepath.Join(dir, "lk")
}

// KubeconfigPathFor returns the per-cluster kubeconfig path for a given name.
func KubeconfigPathFor(name string) string {
	return filepath.Join(KubeconfigDir(), name+".kubeconfig")
}

// LkClusterNames returns the cluster names lk has tracked locally based on
// kubeconfig files at $CUB_CONFIG/lk/*.kubeconfig. Order is sorted by
// filesystem listing.
func LkClusterNames() ([]string, error) {
	entries, err := os.ReadDir(KubeconfigDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lk kubeconfig dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name, ok := strings.CutSuffix(e.Name(), ".kubeconfig")
		if !ok {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// EnsureKubeconfigDir creates the directory if missing.
func EnsureKubeconfigDir() error {
	return os.MkdirAll(KubeconfigDir(), 0o755)
}
