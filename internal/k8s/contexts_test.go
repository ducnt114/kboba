package k8s

import (
	"os"
	"path/filepath"
	"testing"
)

const testKubeconfig = `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev-cluster
  cluster:
    server: https://127.0.0.1:6443
- name: prod-cluster
  cluster:
    server: https://127.0.0.1:7443
contexts:
- name: dev
  context:
    cluster: dev-cluster
    user: dev-user
    namespace: team-a
- name: prod
  context:
    cluster: prod-cluster
    user: prod-user
users:
- name: dev-user
  user:
    token: dev-token
- name: prod-user
  user:
    token: prod-token
`

func writeKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListContextsMarksCurrent(t *testing.T) {
	path := writeKubeconfig(t)

	tests := []struct {
		context     string
		wantCurrent string
	}{
		{context: "", wantCurrent: "dev"}, // falls back to current-context
		{context: "prod", wantCurrent: "prod"},
	}
	for _, tt := range tests {
		c, err := NewClient(path, tt.context)
		if err != nil {
			t.Fatalf("NewClient(%q): %v", tt.context, err)
		}
		got, err := c.ListContexts()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Name != "dev" || got[1].Name != "prod" {
			t.Fatalf("unexpected contexts: %+v", got)
		}
		for _, ci := range got {
			if ci.Current != (ci.Name == tt.wantCurrent) {
				t.Errorf("context=%q: %s.Current = %v", tt.context, ci.Name, ci.Current)
			}
		}
		if got[0].Namespace != "team-a" || got[0].Cluster != "dev-cluster" || got[0].User != "dev-user" {
			t.Errorf("dev context fields wrong: %+v", got[0])
		}
	}
}

func TestNewClientUnknownContextIsNotFatal(t *testing.T) {
	path := writeKubeconfig(t)

	c, err := NewClient(path, "does-not-exist")
	if err != nil {
		t.Fatalf("unknown context should not fail NewClient: %v", err)
	}
	if c.(*client).connErr == nil {
		t.Fatal("expected connErr for unknown context")
	}
	// Contexts must still be listable so the user can switch.
	if ctxs, err := c.ListContexts(); err != nil || len(ctxs) != 2 {
		t.Fatalf("ListContexts = %v, %v", ctxs, err)
	}
}

func TestNewClientDoesNotModifyKubeconfig(t *testing.T) {
	path := writeKubeconfig(t)

	if _, err := NewClient(path, "prod"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testKubeconfig {
		t.Fatal("kubeconfig file was modified")
	}
}

func TestNewClientMissingKubeconfig(t *testing.T) {
	_, err := NewClient(filepath.Join(t.TempDir(), "nope"), "")
	if err == nil {
		t.Fatal("expected error for missing kubeconfig")
	}
}
