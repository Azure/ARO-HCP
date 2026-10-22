// Package detect provides functions to detect the environment the application is running in. It is
// intended to be used either in a main function or automatically used in a program calling init.Service().
package detect

import (
	"context"
	"os"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	typedV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// base is the base environment the application is running in. Set by Init().
var base RunEnv

var baseIs is

// Init initializes the environment detection. This should be called as early as possible in the application.
func Init() {
	if base.alreadyRun {
		return
	}
	defer func() {
		base.alreadyRun = true
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if baseIs.k8(ctx) {
		var err error
		base.IsKubernetes = true
		base.IsKind, err = baseIs.kindCluster(ctx)
		if err != nil {
			base.Err = err
			return
		}
	}
}

// Env returns the environment the application is running in.
func Env() RunEnv {
	return base
}

// RunEnv details the environment the application is running in.
// TODO: Add more fields, like if we are running in other environments, CCP, underlay, ...
type RunEnv struct {
	// IsKubernetes is true if the application is running in a Kubernetes cluster.
	IsKubernetes bool
	// IsKind is true if the application is running in a Kind cluster.
	IsKind bool

	// Err is the error that occurred during the detection of the environment.
	Err error

	// IgnoreTesting is a flag to ignore if testing.Testing() is true for the purpose
	// of .Prod() . Normally being in a test environment makes .Prod() return false. But if
	// you are doing a test with a RunEnv and want Prod() to return true, set this to true.
	// This only works in tests.
	IgnoreTesting bool

	alreadyRun bool
}

// Prod checks if the current cluster is a production cluster.
func (b RunEnv) Prod() bool {
	if testing.Testing() {
		if !b.IgnoreTesting {
			return false
		}
	}
	if b.IsKubernetes && !b.IsKind {
		return true
	}
	return false
}

// is is a helper to detect current cluster types.
type is struct {
	testKind func(ctx context.Context) (bool, error)
}

// k8 checks if the application is running in a Kubernetes cluster.
func (i is) k8(ctx context.Context) bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

// kindCluster checks if the current cluster is a kind cluster.
func (i is) kindCluster(ctx context.Context) (bool, error) {
	if testing.Testing() && i.testKind != nil {
		return i.testKind(ctx)
	}

	lister, err := i.nodeLister(ctx)
	if err != nil {
		return false, err
	}

	nodes, err := lister.List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}

	return i.detectKindFromNodes(nodes), nil
}

// nodeLister returns a nodeLister for the current cluster.
func (i is) nodeLister(ctx context.Context) (typedV1.NodeInterface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientSet.CoreV1().Nodes(), nil
}

func (i is) detectKindFromNodes(nodes *v1.NodeList) bool {
	if nodes == nil {
		return false
	}

	for _, node := range nodes.Items {
		if _, exists := node.Labels["kind.x-k8s.io/cluster"]; exists {
			return true
		}
	}
	return false
}
