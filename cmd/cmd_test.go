package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestUp(t *testing.T) {
	var buf bytes.Buffer
	if err := runUp(&buf, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"test-cluster"`) {
		t.Errorf("expected cluster name in output, got: %s", buf.String())
	}
}

func TestDown(t *testing.T) {
	var buf bytes.Buffer
	if err := runDown(&buf, "test-cluster"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"test-cluster"`) {
		t.Errorf("expected cluster name in output, got: %s", buf.String())
	}
}

func TestList(t *testing.T) {
	var buf bytes.Buffer
	if err := runList(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Error("expected non-empty output")
	}
}

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
