package k8s

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/util/jsonpath"
)

// crdGVR is where CustomResourceDefinitions live. We read them with the
// dynamic client, so we don't need the apiextensions client library.
var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// ResolveResourceType finds a resource type by plural, singular or short
// name, kind, or "plural.group" (e.g. "certificates.cert-manager.io").
// Built-in views win; anything else is found through API discovery and
// shown generically, with a CRD's additionalPrinterColumns when it has any.
func (c *client) ResolveResourceType(ctx context.Context, name string) (*ResourceType, error) {
	if rt, ok := LookupResourceType(name); ok {
		return rt, nil
	}
	if c.connErr != nil {
		return nil, c.connErr
	}

	// Discovery lists every resource the server serves, in each group's
	// preferred version. With some aggregated APIs down it returns partial
	// results plus an error; partial results are good enough here.
	lists, err := discovery.ServerPreferredResources(c.clientset.Discovery())
	if len(lists) == 0 && err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}

	gvr, res, ok := findResource(lists, strings.ToLower(name))
	if !ok {
		return nil, fmt.Errorf("unknown resource type %q", name)
	}
	for _, rt := range ResourceTypes() {
		if rt.gvr == gvr {
			return rt, nil // e.g. "deployments.apps": use the built-in view
		}
	}

	c.discoveredMu.Lock()
	cached := c.discovered[gvr]
	c.discoveredMu.Unlock()
	if cached != nil {
		return cached, nil
	}

	columns := c.printerColumns(ctx, gvr)
	rt := &ResourceType{
		Name:       res.Name,
		Title:      strings.ToUpper(res.Name[:1]) + res.Name[1:],
		Aliases:    res.ShortNames,
		Namespaced: res.Namespaced,
		Columns:    []Column{{"NAME", 0}},
		kind:       res.Kind,
		gvr:        gvr,
		dynamic:    true,
		get:        getUnstructured(gvr),
		convert:    unstructuredResource(columns),
	}
	for _, pc := range columns {
		rt.Columns = append(rt.Columns, Column{Title: pc.title, Width: pc.width})
	}

	c.discoveredMu.Lock()
	defer c.discoveredMu.Unlock()
	if c.discovered == nil {
		c.discovered = map[schema.GroupVersionResource]*ResourceType{}
	}
	if existing := c.discovered[gvr]; existing != nil {
		return existing, nil // resolved concurrently
	}
	c.discovered[gvr] = rt
	return rt, nil
}

// findResource looks name up in discovery results. Only resources that can
// be listed and watched qualify (kboba's tables are informer-driven), and
// subresources such as "pods/log" are skipped.
func findResource(lists []*metav1.APIResourceList, name string) (schema.GroupVersionResource, metav1.APIResource, bool) {
	for _, list := range lists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") || !slices.Contains(r.Verbs, "list") || !slices.Contains(r.Verbs, "watch") {
				continue
			}
			if name == r.Name || name == r.SingularName || name == strings.ToLower(r.Kind) ||
				slices.Contains(r.ShortNames, name) || name == r.Name+"."+gv.Group {
				return gv.WithResource(r.Name), r, true
			}
		}
	}
	return schema.GroupVersionResource{}, metav1.APIResource{}, false
}

// printerColumn is one of a CRD's additionalPrinterColumns: the extra
// columns `kubectl get` shows, each value picked from the object by a
// JSONPath expression.
type printerColumn struct {
	title    string
	width    int
	template string // e.g. "{.status.conditions[?(@.type=="Ready")].status}"
}

// printerColumns reads the additionalPrinterColumns of the CRD defining
// gvr. Built-in resources have no CRD; that, or any error (RBAC may forbid
// reading CRDs), just means "no extra columns".
func (c *client) printerColumns(ctx context.Context, gvr schema.GroupVersionResource) []printerColumn {
	if gvr.Group == "" {
		return nil // core group: never a CRD
	}
	crd, err := c.dynamic.Resource(crdGVR).Get(ctx, gvr.Resource+"."+gvr.Group, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	return parsePrinterColumns(crd.Object, gvr.Version)
}

// parsePrinterColumns extracts the columns of one version from a CRD
// object, skipping wide-only columns (priority > 0), the age column (kboba
// always has AGE) and invalid JSONPaths.
func parsePrinterColumns(crd map[string]any, version string) []printerColumn {
	versions, _, _ := unstructured.NestedSlice(crd, "spec", "versions")
	for _, v := range versions {
		vm, ok := v.(map[string]any)
		if !ok || vm["name"] != version {
			continue
		}
		cols, _, _ := unstructured.NestedSlice(vm, "additionalPrinterColumns")
		var out []printerColumn
		for _, col := range cols {
			cm, ok := col.(map[string]any)
			if !ok {
				continue
			}
			name, _, _ := unstructured.NestedString(cm, "name")
			path, _, _ := unstructured.NestedString(cm, "jsonPath")
			priority, _, _ := unstructured.NestedInt64(cm, "priority")
			if name == "" || path == "" || priority > 0 || path == ".metadata.creationTimestamp" {
				continue
			}
			template := "{" + path + "}"
			if err := jsonpath.New(name).Parse(template); err != nil {
				continue
			}
			title := strings.ToUpper(name)
			out = append(out, printerColumn{title: title, width: min(max(len(title), 10), 30), template: template})
		}
		return out
	}
	return nil
}

// eval renders the column's value for obj. A JSONPath is parsed per call
// because *jsonpath.JSONPath keeps state while executing and is not safe
// for concurrent use; parsing these short expressions is cheap.
func (p printerColumn) eval(obj map[string]any) string {
	jp := jsonpath.New(p.title).AllowMissingKeys(true)
	if err := jp.Parse(p.template); err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := jp.Execute(&buf, obj); err != nil {
		return ""
	}
	return buf.String()
}

func unstructuredResource(columns []printerColumn) func(any) (Resource, bool) {
	return typed(func(u *unstructured.Unstructured) Resource {
		cells := []string{u.GetName()}
		for _, pc := range columns {
			cells = append(cells, pc.eval(u.Object))
		}
		return newResource(u, cells...)
	})
}

func getUnstructured(gvr schema.GroupVersionResource) func(context.Context, *client, string, string) (runtime.Object, error) {
	return func(ctx context.Context, c *client, ns, name string) (runtime.Object, error) {
		return c.dynamic.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	}
}
