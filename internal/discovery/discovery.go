package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"log-download-portal/internal/config"
)

type Resolver interface {
	Start(ctx context.Context)
	Ready(ctx context.Context) error
	Nodes(ctx context.Context) ([]Node, error)
	ResolveAgent(ctx context.Context, nodeName string) (AgentEndpoint, error)
}

type Node struct {
	Name       string `json:"name"`
	NodeReady  bool   `json:"nodeReady"`
	AgentReady bool   `json:"agentReady"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type AgentEndpoint struct {
	NodeName string
	BaseURL  string
}

var (
	ErrNotReady = errors.New("discovery snapshot is not ready")
	ErrStale    = errors.New("discovery snapshot is stale")
	ErrNoAgent  = errors.New("node log agent is unavailable")
	ErrNoNode   = errors.New("node does not exist")
)

func NewResolver(ctx context.Context, cfg *config.Config) (Resolver, error) {
	if cfg.Discovery.Mode == "static" {
		return newStaticResolver(cfg), nil
	}
	return newKubernetesResolver(ctx, cfg)
}

func deriveStatus(nodeReady, agentReady bool) (string, string) {
	switch {
	case agentReady && nodeReady:
		return "Ready", ""
	case agentReady && !nodeReady:
		return "NodeNotReady", "node is not Ready but log agent is reachable"
	case !agentReady:
		return "AgentUnavailable", "node log agent is unavailable"
	default:
		return "Unknown", "node status is unknown"
	}
}

type snapshot struct {
	nodes       map[string]Node
	agents      map[string]AgentEndpoint
	refreshedAt time.Time
}

func (s snapshot) sortedNodes() []Node {
	nodes := make([]Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

type staticResolver struct {
	cfg      *config.Config
	mu       sync.RWMutex
	snapshot snapshot
}

func newStaticResolver(cfg *config.Config) *staticResolver {
	r := &staticResolver{cfg: cfg}
	r.refresh()
	return r
}

func (r *staticResolver) Start(ctx context.Context) {
	<-ctx.Done()
}

func (r *staticResolver) Ready(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.snapshot.refreshedAt.IsZero() {
		return ErrNotReady
	}
	return nil
}

func (r *staticResolver) Nodes(ctx context.Context) ([]Node, error) {
	if err := r.Ready(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.sortedNodes(), nil
}

func (r *staticResolver) ResolveAgent(ctx context.Context, nodeName string) (AgentEndpoint, error) {
	if err := r.Ready(ctx); err != nil {
		return AgentEndpoint{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	node, ok := r.snapshot.nodes[nodeName]
	if !ok {
		return AgentEndpoint{}, ErrNoNode
	}
	if !node.AgentReady {
		return AgentEndpoint{}, ErrNoAgent
	}
	return r.snapshot.agents[nodeName], nil
}

func (r *staticResolver) refresh() {
	next := snapshot{
		nodes:       map[string]Node{},
		agents:      map[string]AgentEndpoint{},
		refreshedAt: time.Now(),
	}
	for _, item := range r.cfg.Discovery.StaticNodes {
		status, reason := deriveStatus(item.NodeReady, item.AgentReady)
		next.nodes[item.Name] = Node{
			Name:       item.Name,
			NodeReady:  item.NodeReady,
			AgentReady: item.AgentReady,
			Status:     status,
			Reason:     reason,
		}
		if item.AgentReady {
			next.agents[item.Name] = AgentEndpoint{NodeName: item.Name, BaseURL: item.AgentURL}
		}
	}
	r.mu.Lock()
	r.snapshot = next
	r.mu.Unlock()
}

type kubernetesResolver struct {
	cfg    *config.Config
	client kubernetes.Interface
	mu     sync.RWMutex
	snap   snapshot
}

func newKubernetesResolver(ctx context.Context, cfg *config.Config) (*kubernetesResolver, error) {
	restConfig, err := kubernetesRESTConfig(cfg.Kubernetes.KubeconfigPath)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return &kubernetesResolver{cfg: cfg, client: client}, nil
}

func kubernetesRESTConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	return rest.InClusterConfig()
}

func (r *kubernetesResolver) Start(ctx context.Context) {
	r.refresh(ctx)
	ticker := time.NewTicker(r.cfg.Discovery.WatchRefreshInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

func (r *kubernetesResolver) Ready(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.snap.refreshedAt.IsZero() {
		return ErrNotReady
	}
	if time.Since(r.snap.refreshedAt) > r.cfg.Discovery.StaleAfter.Duration {
		return ErrStale
	}
	return nil
}

func (r *kubernetesResolver) Nodes(ctx context.Context) ([]Node, error) {
	if err := r.Ready(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap.sortedNodes(), nil
}

func (r *kubernetesResolver) ResolveAgent(ctx context.Context, nodeName string) (AgentEndpoint, error) {
	if err := r.Ready(ctx); err != nil {
		return AgentEndpoint{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	node, ok := r.snap.nodes[nodeName]
	if !ok {
		return AgentEndpoint{}, ErrNoNode
	}
	if !node.AgentReady {
		return AgentEndpoint{}, ErrNoAgent
	}
	endpoint, ok := r.snap.agents[nodeName]
	if !ok {
		return AgentEndpoint{}, ErrNoAgent
	}
	return endpoint, nil
}

func (r *kubernetesResolver) refresh(ctx context.Context) {
	nodes, err := r.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	pods, err := r.client.CoreV1().Pods(r.cfg.Kubernetes.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: r.cfg.Kubernetes.PodSelector,
	})
	if err != nil {
		return
	}

	next := snapshot{
		nodes:       map[string]Node{},
		agents:      map[string]AgentEndpoint{},
		refreshedAt: time.Now(),
	}
	for _, node := range nodes.Items {
		nodeReady := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				nodeReady = true
				break
			}
		}
		status, reason := deriveStatus(nodeReady, false)
		next.nodes[node.Name] = Node{
			Name:      node.Name,
			NodeReady: nodeReady,
			Status:    status,
			Reason:    reason,
		}
	}
	for _, pod := range pods.Items {
		if pod.Spec.NodeName == "" || pod.Status.PodIP == "" || pod.DeletionTimestamp != nil {
			continue
		}
		if !podReady(pod.Status.Conditions) {
			continue
		}
		node, ok := next.nodes[pod.Spec.NodeName]
		if !ok {
			continue
		}
		node.AgentReady = true
		node.Status, node.Reason = deriveStatus(node.NodeReady, true)
		next.nodes[pod.Spec.NodeName] = node
		next.agents[pod.Spec.NodeName] = AgentEndpoint{
			NodeName: pod.Spec.NodeName,
			BaseURL:  agentURL(pod.Status.PodIP, r.cfg.Discovery.AgentPort),
		}
	}

	r.mu.Lock()
	r.snap = next
	r.mu.Unlock()
}

func podReady(conditions []corev1.PodCondition) bool {
	for _, cond := range conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func agentURL(host string, port int) string {
	if stringsContainsColon(host) {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func stringsContainsColon(value string) bool {
	u := url.URL{Host: value}
	return strings.Contains(u.Host, ":")
}
