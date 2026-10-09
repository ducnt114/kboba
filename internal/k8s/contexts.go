package k8s

import "sort"

// ContextInfo describes one kubeconfig context.
type ContextInfo struct {
	Name      string
	Cluster   string
	User      string
	Namespace string // default namespace of the context, "" if unset
	Current   bool   // true for the context this client is bound to
}

func (c *client) ListContexts() ([]ContextInfo, error) {
	out := make([]ContextInfo, 0, len(c.raw.Contexts))
	for name, ctx := range c.raw.Contexts {
		out = append(out, ContextInfo{
			Name:      name,
			Cluster:   ctx.Cluster,
			User:      ctx.AuthInfo,
			Namespace: ctx.Namespace,
			Current:   name == c.contextName,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
