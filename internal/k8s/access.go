package k8s

import (
	"context"
	"fmt"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// Access is an action to check with CanI, like the arguments of
// `kubectl auth can-i <verb> <resource>[/<subresource>] -n <namespace>`.
type Access struct {
	Verb        string // get, list, watch
	Group       string // "" for the core group
	Resource    string // plural, e.g. "pods"
	Subresource string // e.g. "log"
	Namespace   string // "" means all namespaces / cluster-wide
}

func (a Access) String() string {
	res := a.Resource
	if a.Group != "" {
		res += "." + a.Group
	}
	if a.Subresource != "" {
		res += "/" + a.Subresource
	}
	where := "cluster-wide"
	if a.Namespace != "" {
		where = fmt.Sprintf("in namespace %q", a.Namespace)
	}
	return fmt.Sprintf("%s %s %s", a.Verb, res, where)
}

// Decision is the API server's answer to a CanI question.
type Decision struct {
	Allowed bool
	Reason  string // optional explanation from the authorizer
}

// Access describes doing verb on this resource type in namespace.
func (rt *ResourceType) Access(verb, namespace string) Access {
	if !rt.Namespaced {
		namespace = ""
	}
	return Access{Verb: verb, Group: rt.gvr.Group, Resource: rt.gvr.Resource, Namespace: namespace}
}

// LogsAccess describes reading a pod's logs.
func LogsAccess(namespace string) Access {
	return Access{Verb: "get", Resource: "pods", Subresource: "log", Namespace: namespace}
}

// MetricsAccess describes listing metrics for rt (pods or nodes).
func MetricsAccess(rt *ResourceType, namespace string) Access {
	a := rt.Access("list", namespace)
	a.Group = metricsv1beta1.SchemeGroupVersion.Group
	return a
}

// CanI creates a SelfSubjectAccessReview. The API server evaluates it with
// the caller's own identity and returns the verdict; nothing is persisted.
func (c *client) CanI(ctx context.Context, a Access) (Decision, error) {
	if c.connErr != nil {
		return Decision{}, c.connErr
	}
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Verb:        a.Verb,
				Group:       a.Group,
				Resource:    a.Resource,
				Subresource: a.Subresource,
				Namespace:   a.Namespace,
			},
		},
	}
	out, err := c.clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return Decision{}, err
	}
	return Decision{Allowed: out.Status.Allowed, Reason: out.Status.Reason}, nil
}

// ForbiddenError says an action was refused by RBAC before it was tried.
type ForbiddenError struct {
	Access Access
	Reason string
}

func (e *ForbiddenError) Error() string {
	msg := "forbidden: you cannot " + e.Access.String()
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg
}

// CheckAccess returns a *ForbiddenError for the first action c may not
// perform, or nil. If the check itself fails (old server, network, …) it
// returns nil: this is only an early, clearer warning, so it fails open and
// lets the real request report whatever is wrong.
func CheckAccess(ctx context.Context, c Client, actions ...Access) error {
	for _, a := range actions {
		d, err := c.CanI(ctx, a)
		if err != nil {
			return nil
		}
		if !d.Allowed {
			return &ForbiddenError{Access: a, Reason: d.Reason}
		}
	}
	return nil
}
