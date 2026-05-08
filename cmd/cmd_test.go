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

func TestStripSecretsFromManifest(t *testing.T) {
	in := []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: confighub
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: worker
  namespace: confighub
---
apiVersion: v1
kind: Secret
metadata:
  name: confighub-worker-secret
  namespace: confighub
type: Opaque
data:
  token: c2VjcmV0
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  namespace: confighub
spec:
  replicas: 1
`)
	out, err := stripSecretsFromManifest(in)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "kind: Secret") {
		t.Errorf("Secret not stripped:\n%s", got)
	}
	if strings.Contains(got, "c2VjcmV0") {
		t.Errorf("secret data leaked into output:\n%s", got)
	}
	for _, want := range []string{"kind: Namespace", "kind: ServiceAccount", "kind: Deployment"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
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
