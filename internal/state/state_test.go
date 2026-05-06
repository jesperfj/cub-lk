package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CUB_CONFIG", dir)

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Clusters) != 0 {
		t.Fatalf("expected empty, got %d", len(s.Clusters))
	}

	c := Cluster{Name: "alpha", KubeContext: "kind-alpha", SpaceSlug: "alpha-cluster", WorkerSlug: "worker", TargetSlug: "target", CreatedAt: time.Now().UTC().Truncate(time.Second)}
	if err := s.Add(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(c); err == nil {
		t.Fatal("expected duplicate add to fail")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "lk", "state.yaml")); statErr != nil {
		t.Fatalf("expected state file: %v", statErr)
	}

	s2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("alpha")
	if !ok {
		t.Fatal("alpha not found after reload")
	}
	if got.SpaceSlug != "alpha-cluster" {
		t.Errorf("space mismatch: %s", got.SpaceSlug)
	}

	s2.Remove("alpha")
	if _, ok := s2.Get("alpha"); ok {
		t.Fatal("expected removal to take effect")
	}
}
