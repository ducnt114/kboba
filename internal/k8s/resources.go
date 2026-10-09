package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Column describes one column of a resource table. Width 0 means the
// column takes whatever space the others leave.
type Column struct {
	Title string
	Width int
}

// Resource is the UI-friendly summary of any watched object: one table row.
type Resource struct {
	Namespace string // "" for cluster-scoped resources
	Name      string
	Created   time.Time // drives the AGE column (for events: last seen)
	Cells     []string  // one per ResourceType.Columns
	// Pod is set for pods only; the logs and describe views need it.
	Pod *PodInfo
}

// Key uniquely identifies a resource of one type.
func (r Resource) Key() string { return r.Namespace + "/" + r.Name }

// ResourceType describes a kind of object kboba can list. Adding a new
// view means adding one ResourceType: its columns and a convert function.
type ResourceType struct {
	Name       string   // canonical name, also the ":" command (e.g. "pods")
	Title      string   // shown in the table title (e.g. "Pods")
	Aliases    []string // other ":" commands (e.g. "po")
	Namespaced bool
	Columns    []Column // kind-specific columns; the UI adds NAMESPACE and AGE

	kind    string // e.g. "Deployment", for the YAML header
	gvr     schema.GroupVersionResource
	convert func(obj any) (Resource, bool)
	get     func(ctx context.Context, cs kubernetes.Interface, namespace, name string) (runtime.Object, error)
}

var (
	Pods = &ResourceType{
		Name: "pods", Title: "Pods", Aliases: []string{"pod", "po"}, Namespaced: true,
		Columns: []Column{{"NAME", 0}, {"READY", 7}, {"STATUS", 22}, {"RESTARTS", 9}},
		kind:    "Pod",
		gvr:     corev1.SchemeGroupVersion.WithResource("pods"),
		convert: typed(podResource),
		get: func(ctx context.Context, cs kubernetes.Interface, ns, name string) (runtime.Object, error) {
			return cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		},
	}
	Deployments = &ResourceType{
		Name: "deployments", Title: "Deployments", Aliases: []string{"deployment", "deploy", "dp"}, Namespaced: true,
		Columns: []Column{{"NAME", 0}, {"READY", 9}, {"UP-TO-DATE", 10}, {"AVAILABLE", 9}},
		kind:    "Deployment",
		gvr:     appsv1.SchemeGroupVersion.WithResource("deployments"),
		convert: typed(deploymentResource),
		get: func(ctx context.Context, cs kubernetes.Interface, ns, name string) (runtime.Object, error) {
			return cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		},
	}
	Services = &ResourceType{
		Name: "services", Title: "Services", Aliases: []string{"service", "svc"}, Namespaced: true,
		Columns: []Column{{"NAME", 0}, {"TYPE", 12}, {"CLUSTER-IP", 15}, {"EXTERNAL-IP", 15}, {"PORT(S)", 22}},
		kind:    "Service",
		gvr:     corev1.SchemeGroupVersion.WithResource("services"),
		convert: typed(serviceResource),
		get: func(ctx context.Context, cs kubernetes.Interface, ns, name string) (runtime.Object, error) {
			return cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
		},
	}
	Events = &ResourceType{
		Name: "events", Title: "Events", Aliases: []string{"event", "ev"}, Namespaced: true,
		Columns: []Column{{"TYPE", 8}, {"REASON", 20}, {"OBJECT", 30}, {"MESSAGE", 0}},
		kind:    "Event",
		gvr:     corev1.SchemeGroupVersion.WithResource("events"),
		convert: typed(eventResource),
		get: func(ctx context.Context, cs kubernetes.Interface, ns, name string) (runtime.Object, error) {
			return cs.CoreV1().Events(ns).Get(ctx, name, metav1.GetOptions{})
		},
	}
	Nodes = &ResourceType{
		Name: "nodes", Title: "Nodes", Aliases: []string{"node", "no"}, Namespaced: false,
		Columns: []Column{{"NAME", 0}, {"STATUS", 26}, {"ROLES", 16}, {"VERSION", 12}},
		kind:    "Node",
		gvr:     corev1.SchemeGroupVersion.WithResource("nodes"),
		convert: typed(nodeResource),
		get: func(ctx context.Context, cs kubernetes.Interface, ns, name string) (runtime.Object, error) {
			return cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		},
	}
)

// ResourceTypes lists every resource type kboba knows.
func ResourceTypes() []*ResourceType {
	return []*ResourceType{Pods, Deployments, Services, Events, Nodes}
}

// LookupResourceType finds a type by name or alias.
func LookupResourceType(name string) (*ResourceType, bool) {
	for _, rt := range ResourceTypes() {
		if rt.Name == name {
			return rt, true
		}
		for _, a := range rt.Aliases {
			if a == name {
				return rt, true
			}
		}
	}
	return nil, false
}

// typed adapts a converter for one concrete type to the informer's `any`.
func typed[T any](f func(T) Resource) func(any) (Resource, bool) {
	return func(obj any) (Resource, bool) {
		o, ok := obj.(T)
		if !ok {
			return Resource{}, false
		}
		return f(o), true
	}
}

func newResource(m metav1.Object, cells ...string) Resource {
	return Resource{
		Namespace: m.GetNamespace(),
		Name:      m.GetName(),
		Created:   m.GetCreationTimestamp().Time,
		Cells:     cells,
	}
}

func podResource(p *corev1.Pod) Resource {
	info := NewPodInfo(p)
	r := newResource(p, info.Name, info.Ready, info.Status, fmt.Sprint(info.Restarts))
	r.Pod = &info
	return r
}

func deploymentResource(d *appsv1.Deployment) Resource {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	s := d.Status
	return newResource(d, d.Name,
		fmt.Sprintf("%d/%d", s.ReadyReplicas, desired),
		fmt.Sprint(s.UpdatedReplicas),
		fmt.Sprint(s.AvailableReplicas),
	)
}

func serviceResource(s *corev1.Service) Resource {
	return newResource(s, s.Name,
		string(s.Spec.Type),
		orNone(s.Spec.ClusterIP),
		serviceExternalIP(s),
		servicePorts(s.Spec.Ports),
	)
}

func serviceExternalIP(s *corev1.Service) string {
	var ips []string
	for _, in := range s.Status.LoadBalancer.Ingress {
		if in.IP != "" {
			ips = append(ips, in.IP)
		} else if in.Hostname != "" {
			ips = append(ips, in.Hostname)
		}
	}
	ips = append(ips, s.Spec.ExternalIPs...)
	switch {
	case len(ips) > 0:
		return strings.Join(ips, ",")
	case s.Spec.Type == corev1.ServiceTypeLoadBalancer:
		return "<pending>"
	case s.Spec.Type == corev1.ServiceTypeExternalName:
		return s.Spec.ExternalName
	}
	return "<none>"
}

// servicePorts formats ports like kubectl: "80/TCP,443:30443/TCP".
func servicePorts(ports []corev1.ServicePort) string {
	if len(ports) == 0 {
		return "<none>"
	}
	out := make([]string, len(ports))
	for i, p := range ports {
		if p.NodePort != 0 {
			out[i] = fmt.Sprintf("%d:%d/%s", p.Port, p.NodePort, p.Protocol)
		} else {
			out[i] = fmt.Sprintf("%d/%s", p.Port, p.Protocol)
		}
	}
	return strings.Join(out, ",")
}

func eventResource(ev *corev1.Event) Resource {
	obj := strings.ToLower(ev.InvolvedObject.Kind) + "/" + ev.InvolvedObject.Name
	r := newResource(ev, ev.Type, ev.Reason, obj, strings.TrimSpace(ev.Message))
	r.Created = eventTime(*ev) // AGE of an event means "last seen"
	return r
}

func nodeResource(n *corev1.Node) Resource {
	status := "Unknown"
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				status = "Ready"
			} else {
				status = "NotReady"
			}
		}
	}
	if n.Spec.Unschedulable {
		status += ",SchedulingDisabled"
	}
	return newResource(n, n.Name, status, nodeRoles(n.Labels), orNone(n.Status.NodeInfo.KubeletVersion))
}

func nodeRoles(labels map[string]string) string {
	const prefix = "node-role.kubernetes.io/"
	var roles []string
	for k := range labels {
		if role, ok := strings.CutPrefix(k, prefix); ok && role != "" {
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		return "<none>"
	}
	sort.Strings(roles)
	return strings.Join(roles, ",")
}

// ResourceEventType says what a ResourceEvent means.
type ResourceEventType int

const (
	Upserted    ResourceEventType = iota // an object was added or changed
	Deleted                              // an object is gone
	Synced                               // the initial list has been delivered
	WatchFailed                          // listing/watching failed; Err is set. The informer keeps retrying.
)

// ResourceEvent is one change delivered by a ResourceWatch.
type ResourceEvent struct {
	Type     ResourceEventType
	Resource Resource
	Err      error
}

// ResourceWatch is a running informer.
type ResourceWatch struct {
	// Events delivers changes. It is closed after Stop, once the informer
	// has fully shut down, so readers blocked on it are released.
	Events <-chan ResourceEvent
	// Stop shuts the informer down. It is safe to call more than once and
	// never blocks.
	Stop func()
}

// WatchResources starts an informer for rt in namespace ("" means all
// namespaces; ignored for cluster-scoped types). The informer first lists
// the objects (delivered as Upserted events, followed by Synced) and then
// watches for changes.
func (c *client) WatchResources(rt *ResourceType, namespace string) (*ResourceWatch, error) {
	if c.connErr != nil {
		return nil, c.connErr
	}
	if !rt.Namespaced {
		namespace = metav1.NamespaceAll
	}

	events := make(chan ResourceEvent, 256)
	stopCh := make(chan struct{})

	// send never blocks forever: once stopCh is closed it gives up.
	send := func(ev ResourceEvent) {
		select {
		case events <- ev:
		case <-stopCh:
		}
	}
	sendObj := func(typ ResourceEventType, obj any) {
		if r, ok := rt.convert(obj); ok {
			send(ResourceEvent{Type: typ, Resource: r})
		}
	}

	factory := informers.NewSharedInformerFactoryWithOptions(c.clientset, 0, informers.WithNamespace(namespace))
	generic, err := factory.ForResource(rt.gvr)
	if err != nil {
		return nil, err
	}
	informer := generic.Informer()

	// Without this, list/watch errors (e.g. RBAC forbidden) would only be
	// logged by client-go.
	err = informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		send(ResourceEvent{Type: WatchFailed, Err: err})
	})
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { sendObj(Upserted, obj) },
		UpdateFunc: func(_, obj any) { sendObj(Upserted, obj) },
		DeleteFunc: func(obj any) {
			// When the watch missed the delete, we get a tombstone.
			if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tomb.Obj
			}
			sendObj(Deleted, obj)
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
			send(ResourceEvent{Type: Synced})
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
	return &ResourceWatch{Events: events, Stop: stop}, nil
}
