package k8s

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ListResources returns a snapshot of rt's objects in namespace ("" for
// all), optionally filtered by a label selector, sorted by key. Tables use
// informers; this is for one-off views such as the ownership tree.
func (c *client) ListResources(ctx context.Context, rt *ResourceType, namespace, labelSelector string) ([]Resource, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}
	if !rt.Namespaced {
		namespace = ""
	}
	objs, err := rt.list(ctx, c, namespace, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(objs))
	for _, o := range objs {
		if r, ok := rt.convert(o); ok {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

// TreeNode is one object in an ownership tree.
type TreeNode struct {
	Type     *ResourceType
	Resource Resource
	Children []*TreeNode
}

// Level is a set of candidate children for one depth of a tree.
type Level struct {
	Type  *ResourceType
	Items []Resource
}

// OwnerTree builds the tree below root: objects of levels[0] owned by root
// (metadata.ownerReferences), objects of levels[1] owned by those, and so
// on. Links go by UID, like the garbage collector's: names can be reused,
// UIDs can't.
func OwnerTree(rootType *ResourceType, root Resource, levels ...Level) *TreeNode {
	top := &TreeNode{Type: rootType, Resource: root}
	parents := []*TreeNode{top}
	for _, lvl := range levels {
		byUID := make(map[string]*TreeNode, len(parents))
		for _, p := range parents {
			byUID[p.Resource.UID] = p
		}
		var next []*TreeNode
		for _, item := range lvl.Items {
			for _, o := range item.Owners {
				if p, ok := byUID[o.UID]; ok && o.UID != "" {
					child := &TreeNode{Type: lvl.Type, Resource: item}
					p.Children = append(p.Children, child)
					next = append(next, child)
					break
				}
			}
		}
		parents = next
	}
	return top
}
