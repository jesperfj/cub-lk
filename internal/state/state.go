// Package state persists the set of clusters lk has created. The file
// lives at ${CUB_CONFIG:-$HOME/.confighub}/lk/state.yaml.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

type Cluster struct {
	Name        string    `yaml:"name"`
	KubeContext string    `yaml:"kubeContext"`
	SpaceSlug   string    `yaml:"spaceSlug"`
	WorkerSlug  string    `yaml:"workerSlug"`
	TargetSlug  string    `yaml:"targetSlug"`
	UnitSlug    string    `yaml:"unitSlug,omitempty"`
	CreatedAt   time.Time `yaml:"createdAt"`
}

type State struct {
	Clusters []Cluster `yaml:"clusters"`

	path string
}

// Path returns the on-disk location of the state file.
func Path() string {
	dir := os.Getenv("CUB_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".confighub")
	}
	return filepath.Join(dir, "lk", "state.yaml")
}

// Load reads the state file. Missing file returns an empty State.
func Load() (*State, error) {
	p := Path()
	s := &State{path: p}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := yaml.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	s.path = p
	return s, nil
}

// Save writes state atomically.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// Get returns the cluster with the given name, or false if absent.
func (s *State) Get(name string) (Cluster, bool) {
	for _, c := range s.Clusters {
		if c.Name == name {
			return c, true
		}
	}
	return Cluster{}, false
}

// Add appends a cluster. Returns an error if the name already exists.
func (s *State) Add(c Cluster) error {
	if _, ok := s.Get(c.Name); ok {
		return fmt.Errorf("cluster %q already tracked", c.Name)
	}
	s.Clusters = append(s.Clusters, c)
	sort.SliceStable(s.Clusters, func(i, j int) bool { return s.Clusters[i].Name < s.Clusters[j].Name })
	return nil
}

// Remove deletes a cluster entry. No-op if absent.
func (s *State) Remove(name string) {
	out := s.Clusters[:0]
	for _, c := range s.Clusters {
		if c.Name != name {
			out = append(out, c)
		}
	}
	s.Clusters = out
}
