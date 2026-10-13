package k8s

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/duration"
)

// Describe returns a human-readable description of any object: metadata,
// owners, status conditions and the object's events. Pods get the richer
// DescribePod output.
//
// It works the same for built-in types and CRDs because it reads the
// object as unstructured data: conditions are found at status.conditions
// whatever the kind, by convention of the Kubernetes API.
func (c *client) Describe(ctx context.Context, rt *ResourceType, namespace, name string) (string, error) {
	if rt == Pods {
		return c.DescribePod(ctx, namespace, name)
	}
	if c.connErr != nil {
		return "", c.connErr
	}
	if !rt.Namespaced {
		namespace = ""
	}
	obj, err := rt.get(ctx, c, namespace, name)
	if err != nil {
		return "", err
	}
	u, err := toUnstructured(obj)
	if err != nil {
		return "", err
	}
	events, evErr := c.eventsFor(ctx, u)
	return formatDescription(rt, u, events, evErr, time.Now()), nil
}

// toUnstructured gives a generic map view of a typed or unstructured object.
func toUnstructured(obj runtime.Object) (*unstructured.Unstructured, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u, nil
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

// eventsFor lists the events about one object, matched by UID: a deleted
// and re-created object with the same name has a new UID, so its
// predecessor's events don't show up.
func (c *client) eventsFor(ctx context.Context, u *unstructured.Unstructured) ([]corev1.Event, error) {
	uid := string(u.GetUID())
	selector := fields.OneTermEqualSelector("involvedObject.uid", uid).String()
	// Events of cluster-scoped objects (nodes) live in some namespace
	// ("default" for nodes), so search all namespaces for those.
	list, err := c.clientset.CoreV1().Events(u.GetNamespace()).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, err
	}
	var out []corev1.Event
	for _, ev := range list.Items {
		if string(ev.InvolvedObject.UID) == uid { // not every server/fake honours the selector
			out = append(out, ev)
		}
	}
	return out, nil
}

func formatDescription(rt *ResourceType, u *unstructured.Unstructured, events []corev1.Event, eventsErr error, now time.Time) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 8, 2, ' ', 0)
	age := func(t time.Time) string {
		if t.IsZero() {
			return "<unknown>"
		}
		return duration.HumanDuration(now.Sub(t))
	}

	field(w, "Name", u.GetName())
	if u.GetNamespace() != "" {
		field(w, "Namespace", u.GetNamespace())
	}
	field(w, "Kind", rt.kind)
	field(w, "API Version", rt.gvr.GroupVersion().String())
	created := u.GetCreationTimestamp().Time
	field(w, "Created", fmt.Sprintf("%s (%s ago)", created.Format(time.RFC1123Z), age(created)))
	field(w, "Labels", formatMap(u.GetLabels()))
	field(w, "Annotations", formatMap(describableAnnotations(u.GetAnnotations())))
	if owners := u.GetOwnerReferences(); len(owners) > 0 {
		var refs []string
		for _, o := range owners {
			refs = append(refs, o.Kind+"/"+o.Name)
		}
		field(w, "Controlled By", strings.Join(refs, ", "))
	}

	writeConditions(w, u.Object, age)
	writeEvents(w, events, eventsErr, age)
	w.Flush()
	return b.String()
}

// describableAnnotations drops kubectl's last-applied-configuration: it is
// a full copy of the object (for a Secret, including its values).
func describableAnnotations(ann map[string]string) map[string]string {
	out := make(map[string]string, len(ann))
	for k, v := range ann {
		if k == "kubectl.kubernetes.io/last-applied-configuration" {
			continue
		}
		if len(v) > 80 {
			v = v[:77] + "..."
		}
		out[k] = v
	}
	return out
}

// writeConditions prints status.conditions, the convention most kinds
// (and well-behaved CRDs) use to report their state.
func writeConditions(w io.Writer, obj map[string]any, age func(time.Time) string) {
	conds, found, _ := unstructured.NestedSlice(obj, "status", "conditions")
	fmt.Fprintln(w, "Conditions:")
	if !found || len(conds) == 0 {
		fmt.Fprintln(w, "  <none>")
		return
	}
	type row struct{ typ, status, reason, since, message string }
	var rows []row
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		str := func(k string) string { v, _, _ := unstructured.NestedString(cm, k); return v }
		since := ""
		if t, err := time.Parse(time.RFC3339, str("lastTransitionTime")); err == nil {
			since = age(t)
		}
		rows = append(rows, row{str("type"), str("status"), str("reason"), since, str("message")})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].typ < rows[j].typ })
	fmt.Fprintln(w, "  Type\tStatus\tReason\tSince\tMessage")
	for _, r := range rows {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", r.typ, r.status, orNone(r.reason), orNone(r.since), r.message)
	}
}
