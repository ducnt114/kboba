package k8s

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
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
	obj, err := rt.get(ctx, c.clientset, namespace, name)
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

	out, err := yaml.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
