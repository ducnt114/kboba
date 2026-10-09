package k8s

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// GetYAML fetches one object and renders it as YAML, like
// `kubectl get -o yaml` but without managedFields, which are long and
// rarely useful when reading.
func (c *client) GetYAML(ctx context.Context, rt *ResourceType, namespace, name string) (string, error) {
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
	return toYAML(rt, obj)
}

// toYAML modifies obj in place, so obj must be a fresh copy from the API,
// never an object from an informer cache (those are shared).
func toYAML(rt *ResourceType, obj runtime.Object) (string, error) {
	// Typed clients return objects with an empty TypeMeta; restore it so
	// the YAML starts with apiVersion and kind like kubectl's.
	obj.GetObjectKind().SetGroupVersionKind(rt.gvr.GroupVersion().WithKind(rt.kind))

	if m, err := meta.Accessor(obj); err == nil {
		m.SetManagedFields(nil)
	}
	if rt.gvr == secretsGVR {
		redactSecret(obj)
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var secretsGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

// redactSecret hides a Secret's values. Reading them is allowed (it's a
// get), but a TUI is easily screen-shared, so kboba shows only the keys and
// sizes. The last-applied annotation is dropped too: it holds the values.
func redactSecret(obj runtime.Object) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	for _, field := range []string{"data", "stringData"} {
		values, found, _ := unstructured.NestedMap(u.Object, field)
		if !found {
			continue
		}
		for k, v := range values {
			n := 0
			if str, ok := v.(string); ok {
				n = len(str)
			}
			values[k] = fmt.Sprintf("<redacted, %d chars>", n)
		}
		_ = unstructured.SetNestedMap(u.Object, values, field)
	}
	if ann := u.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		u.SetAnnotations(ann)
	}
}
