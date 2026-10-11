package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

// allowOnly makes the fake API server answer SelfSubjectAccessReviews: only
// the listed actions ("verb resource/subresource namespace") are allowed.
func allowOnly(t *testing.T, c *client, allowed ...string) {
	t.Helper()
	cs := c.clientset.(interface {
		PrependReactor(verb, resource string, reaction k8stesting.ReactionFunc)
	})
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review := action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		ra := review.Spec.ResourceAttributes
		key := ra.Verb + " " + ra.Resource
		if ra.Subresource != "" {
			key += "/" + ra.Subresource
		}
		key += " " + ra.Namespace
		for _, a := range allowed {
			if a == key {
				review.Status.Allowed = true
			}
		}
		if !review.Status.Allowed {
			review.Status.Reason = "no RBAC rule"
		}
		return true, review, nil
	})
}

func TestCanI(t *testing.T) {
	c, cs := newFakeClient(t)
	allowOnly(t, c, "list pods a", "get pods/log a")
	ctx := context.Background()

	tests := []struct {
		access Access
		want   bool
	}{
		{Pods.Access("list", "a"), true},
		{Pods.Access("list", "b"), false},
		{Pods.Access("watch", "a"), false},
		{LogsAccess("a"), true},
		{Nodes.Access("list", "a"), false}, // cluster-scoped: namespace dropped
	}
	for _, tt := range tests {
		d, err := c.CanI(ctx, tt.access)
		if err != nil {
			t.Fatal(err)
		}
		if d.Allowed != tt.want {
			t.Errorf("CanI(%s) = %v, want %v", tt.access, d.Allowed, tt.want)
		}
	}

	// Every request was a create of a selfsubjectaccessreview — nothing else.
	for _, a := range cs.Actions() {
		if a.GetVerb() != "create" || a.GetResource().Resource != "selfsubjectaccessreviews" {
			t.Errorf("unexpected action %s %s", a.GetVerb(), a.GetResource())
		}
	}
}

func TestAccessDescriptions(t *testing.T) {
	tests := map[string]Access{
		`list pods in namespace "a"`:                Pods.Access("list", "a"),
		`watch deployments.apps cluster-wide`:       Deployments.Access("watch", ""),
		`list nodes cluster-wide`:                   Nodes.Access("list", "a"),
		`get pods/log in namespace "a"`:             LogsAccess("a"),
		`list pods.metrics.k8s.io in namespace "a"`: MetricsAccess(Pods, "a"),
	}
	for want, a := range tests {
		if got := a.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// fakeCanI is a Client that only implements CanI.
type fakeCanI struct {
	Client
	allowed map[string]bool
	err     error
}

func (f fakeCanI) CanI(_ context.Context, a Access) (Decision, error) {
	return Decision{Allowed: f.allowed[a.Verb], Reason: "nope"}, f.err
}

func TestCheckAccess(t *testing.T) {
	ctx := context.Background()
	list, watch := Pods.Access("list", "a"), Pods.Access("watch", "a")

	if err := CheckAccess(ctx, fakeCanI{allowed: map[string]bool{"list": true, "watch": true}}, list, watch); err != nil {
		t.Fatalf("all allowed: %v", err)
	}

	err := CheckAccess(ctx, fakeCanI{allowed: map[string]bool{"list": true}}, list, watch)
	var fe *ForbiddenError
	if !errors.As(err, &fe) || fe.Access != watch {
		t.Fatalf("err = %v, want ForbiddenError for watch", err)
	}
	if !strings.Contains(err.Error(), `cannot watch pods in namespace "a" (nope)`) {
		t.Fatalf("message = %q", err.Error())
	}

	// If the check itself fails we can't tell: fail open.
	if err := CheckAccess(ctx, fakeCanI{err: errors.New("boom")}, list); err != nil {
		t.Fatalf("a failing check must not block: %v", err)
	}
}
