package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpRunsAllSubcommands(t *testing.T) {
	r := newRootCmd()
	wantCmds := []string{"up", "down", "list", "version"}
	for _, want := range wantCmds {
		found := false
		for _, sc := range r.Commands() {
			if sc.Name() == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestVersionPrints(t *testing.T) {
	r := newRootCmd()
	var buf bytes.Buffer
	r.SetOut(&buf)
	r.SetArgs([]string{"version"})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "lk ") {
		t.Errorf("expected version output, got %q", buf.String())
	}
}

func TestListEmpty(t *testing.T) {
	t.Setenv("CUB_CONFIG", t.TempDir())
	r := newRootCmd()
	var buf bytes.Buffer
	r.SetOut(&buf)
	r.SetErr(&buf)
	r.SetArgs([]string{"list"})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No lk clusters tracked") {
		t.Errorf("expected empty-list message, got %q", buf.String())
	}
}
