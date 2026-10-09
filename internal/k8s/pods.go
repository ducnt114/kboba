package k8s

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// defaultContainerAnnotation is the annotation kubectl uses to choose the
// container for logs/exec when a pod has several.
const defaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

// PodInfo is the UI-friendly summary of a pod: everything the pods table
// needs, already formatted the way kubectl shows it.
type PodInfo struct {
	Namespace  string
	Name       string
	Ready      string // e.g. "1/2"
	Status     string // e.g. "Running", "CrashLoopBackOff", "Init:0/1"
	Restarts   int32
	Created    time.Time
	Containers []string // regular containers, in spec order
	// DefaultContainer is the container to show logs for by default.
	DefaultContainer string
}

// Key uniquely identifies a pod across namespaces.
func (p PodInfo) Key() string { return p.Namespace + "/" + p.Name }

// NewPodInfo summarises a pod.
func NewPodInfo(pod *corev1.Pod) PodInfo {
	info := PodInfo{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		Created:   pod.CreationTimestamp.Time,
		Status:    podStatus(pod),
	}

	ready := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
		info.Restarts += cs.RestartCount
	}
	info.Ready = fmt.Sprintf("%d/%d", ready, len(pod.Spec.Containers))

	for _, c := range pod.Spec.Containers {
		info.Containers = append(info.Containers, c.Name)
	}
	if len(info.Containers) > 0 {
		info.DefaultContainer = info.Containers[0]
	}
	if name, ok := pod.Annotations[defaultContainerAnnotation]; ok {
		for _, c := range info.Containers {
			if c == name {
				info.DefaultContainer = name
			}
		}
	}
	return info
}

// podStatus mirrors (a simplified version of) the STATUS column of
// `kubectl get pods`.
func podStatus(pod *corev1.Pod) string {
	if pod.DeletionTimestamp != nil {
		return "Terminating"
	}

	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}
	if reason == "" {
		reason = "Pending"
	}

	// Init containers run first; while one hasn't succeeded it dominates.
	for i, cs := range pod.Status.InitContainerStatuses {
		switch {
		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode == 0:
			continue
		case cs.State.Terminated != nil:
			return "Init:" + terminatedReason(cs.State.Terminated)
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing":
			return "Init:" + cs.State.Waiting.Reason
		default:
			return fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
		}
	}

	// A waiting or terminated container explains more than the phase does
	// (e.g. CrashLoopBackOff, ImagePullBackOff, OOMKilled). Like kubectl, the
	// last such container wins.
	running := false
	for _, cs := range pod.Status.ContainerStatuses {
		switch {
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
			reason = cs.State.Waiting.Reason
		case cs.State.Terminated != nil:
			reason = terminatedReason(cs.State.Terminated)
		case cs.State.Running != nil && cs.Ready:
			running = true
		}
	}
	// Some containers completed while others still run.
	if reason == "Completed" && running {
		reason = "Running"
	}
	return reason
}

func terminatedReason(t *corev1.ContainerStateTerminated) string {
	switch {
	case t.Reason != "":
		return t.Reason
	case t.Signal != 0:
		return fmt.Sprintf("Signal:%d", t.Signal)
	default:
		return fmt.Sprintf("ExitCode:%d", t.ExitCode)
	}
}

func (c *client) ListPods(ctx context.Context, namespace string) ([]PodInfo, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}
	list, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]PodInfo, len(list.Items))
	for i := range list.Items {
		out[i] = NewPodInfo(&list.Items[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

func (c *client) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}
	return c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}

// PodEventType says what a PodEvent means.
type PodEventType int

const (
	PodUpserted    PodEventType = iota // a pod was added or changed
	PodDeleted                         // a pod is gone
	PodsSynced                         // the initial list has been delivered
	PodWatchFailed                     // listing/watching failed; Err is set. The informer keeps retrying.
)

// PodEvent is one change delivered by a PodWatch.
type PodEvent struct {
	Type PodEventType
	Pod  PodInfo
	Err  error
}

// PodWatch is a running pod informer.
type PodWatch struct {
	// Events delivers changes. It is closed after Stop, once the informer
	// has fully shut down, so readers blocked on it are released.
	Events <-chan PodEvent
	// Stop shuts the informer down. It is safe to call more than once and
	// never blocks.
	Stop func()
}

// WatchPods starts a pod informer for namespace ("" means all namespaces).
// The informer first lists the pods (delivered as PodUpserted events,
// followed by PodsSynced) and then watches for changes.
func (c *client) WatchPods(namespace string) (*PodWatch, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}

	events := make(chan PodEvent, 256)
	stopCh := make(chan struct{})

	// send never blocks forever: once stopCh is closed it gives up.
	send := func(ev PodEvent) {
		select {
		case events <- ev:
		case <-stopCh:
		}
	}

	factory := informers.NewSharedInformerFactoryWithOptions(c.clientset, 0, informers.WithNamespace(namespace))
	informer := factory.Core().V1().Pods().Informer()

	// Without this, list/watch errors (e.g. RBAC forbidden) would only be
	// logged by client-go.
	err := informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		send(PodEvent{Type: PodWatchFailed, Err: err})
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				send(PodEvent{Type: PodUpserted, Pod: NewPodInfo(pod)})
			}
		},
		UpdateFunc: func(_, obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				send(PodEvent{Type: PodUpserted, Pod: NewPodInfo(pod)})
			}
		},
		DeleteFunc: func(obj any) {
			// When the watch missed the delete, we get a tombstone.
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			if pod, ok := obj.(*corev1.Pod); ok {
				send(PodEvent{Type: PodDeleted, Pod: NewPodInfo(pod)})
			}
		},
	})
	if err != nil {
		return nil, err
	}

	factory.Start(stopCh)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if cache.WaitForCacheSync(stopCh, informer.HasSynced) {
			send(PodEvent{Type: PodsSynced})
		}
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopCh)
			// Shutdown waits for the informer goroutines (and therefore our
			// handlers) to exit. Only then is it safe to close events.
			go func() {
				factory.Shutdown()
				wg.Wait()
				close(events)
			}()
		})
	}
	return &PodWatch{Events: events, Stop: stop}, nil
}
