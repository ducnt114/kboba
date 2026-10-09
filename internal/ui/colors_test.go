package ui

import (
	"testing"

	"github.com/ducnt114/kboba/internal/k8s"
)

func TestStatusHealth(t *testing.T) {
	tests := map[string]health{
		"Running":                  healthy,
		"Ready":                    healthy,
		"CrashLoopBackOff":         failing,
		"ImagePullBackOff":         failing,
		"ErrImagePull":             failing,
		"Error":                    failing,
		"OOMKilled":                failing,
		"ExitCode:2":               failing,
		"Init:Error":               failing, // failure wins over "Init:"
		"Init:CrashLoopBackOff":    failing,
		"NotReady":                 failing, // not "Ready"
		"Pending":                  pending,
		"ContainerCreating":        pending,
		"Init:0/1":                 pending,
		"Terminating":              pending,
		"Ready,SchedulingDisabled": pending,
		"Completed":                finished,
		"Succeeded":                finished,
	}
	for status, want := range tests {
		if got := statusHealth(status); got != want {
			t.Errorf("statusHealth(%q) = %d, want %d", status, got, want)
		}
	}
}

func TestRowHealth(t *testing.T) {
	pod := func(status, ready string) k8s.Resource {
		return k8s.Resource{Pod: &k8s.PodInfo{Status: status, Ready: ready}}
	}
	tests := []struct {
		name string
		rt   *k8s.ResourceType
		r    k8s.Resource
		want health
	}{
		{"running and ready", k8s.Pods, pod("Running", "2/2"), healthy},
		{"running but not ready", k8s.Pods, pod("Running", "1/2"), pending},
		{"crashloop", k8s.Pods, pod("CrashLoopBackOff", "0/1"), failing},
		{"deployment rolling", k8s.Deployments, k8s.Resource{Cells: []string{"web", "1/3"}}, pending},
		{"deployment ready", k8s.Deployments, k8s.Resource{Cells: []string{"web", "3/3"}}, healthy},
		{"node not ready", k8s.Nodes, k8s.Resource{Cells: []string{"n1", "NotReady"}}, failing},
		{"warning event", k8s.Events, k8s.Resource{Cells: []string{"Warning"}}, pending},
		{"normal event", k8s.Events, k8s.Resource{Cells: []string{"Normal"}}, healthy},
		{"services are never coloured", k8s.Services, k8s.Resource{Cells: []string{"web"}}, healthy},
	}
	for _, tt := range tests {
		if got := rowHealth(tt.rt, tt.r); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestAllReady(t *testing.T) {
	for in, want := range map[string]bool{"1/1": true, "0/1": false, "2/3": false, "0/0": true, "n/a": true, "": true} {
		if got := allReady(in); got != want {
			t.Errorf("allReady(%q) = %v", in, got)
		}
	}
}
