package k8s

import (
	"context"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *client) ListNamespaces(ctx context.Context) ([]string, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}
	list, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(list.Items))
	for i, ns := range list.Items {
		names[i] = ns.Name
	}
	sort.Strings(names)
	return names, nil
}
