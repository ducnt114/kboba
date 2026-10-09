package k8s

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Usage is the current resource usage of a pod (summed over its
// containers) or a node, as reported by metrics-server.
type Usage struct {
	CPUMilli    int64 // millicores
	MemoryBytes int64
}

// ErrNoMetrics is returned for resource types metrics-server doesn't cover.
var ErrNoMetrics = errors.New("no metrics for this resource type")

// ListMetrics returns current usage keyed like Resource.Key(), for pods
// (in namespace, filtered by labelSelector) or nodes.
//
// Unlike everything else in kboba this can't be watched: the metrics API
// only supports get and list, so callers have to poll.
func (c *client) ListMetrics(ctx context.Context, rt *ResourceType, namespace, labelSelector string) (map[string]Usage, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}
	opts := metav1.ListOptions{LabelSelector: labelSelector}
	out := map[string]Usage{}

	switch rt {
	case Pods:
		list, err := c.metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, opts)
		if err != nil {
			return nil, metricsErr(err)
		}
		for _, pm := range list.Items {
			var u Usage
			for _, ct := range pm.Containers {
				u.CPUMilli += ct.Usage.Cpu().MilliValue()
				u.MemoryBytes += ct.Usage.Memory().Value()
			}
			out[pm.Namespace+"/"+pm.Name] = u
		}
	case Nodes:
		list, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, opts)
		if err != nil {
			return nil, metricsErr(err)
		}
		for _, nm := range list.Items {
			out["/"+nm.Name] = Usage{
				CPUMilli:    nm.Usage.Cpu().MilliValue(),
				MemoryBytes: nm.Usage.Memory().Value(),
			}
		}
	default:
		return nil, ErrNoMetrics
	}
	return out, nil
}

// metricsErr explains the most common failure: metrics-server isn't
// installed, so the metrics.k8s.io API doesn't exist.
func metricsErr(err error) error {
	return fmt.Errorf("metrics unavailable (is metrics-server installed?): %w", err)
}
