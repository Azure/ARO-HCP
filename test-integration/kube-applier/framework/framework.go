// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package framework is the artifact-driven test harness for kube-applier
// integration tests.
//
// A test case is a directory under artifacts/. Inside, each subdirectory is
// a numbered step:
//
//	NN-stepType-description/
//
// where NN orders execution and stepType selects the step kind. See the
// step_*.go files in this package for available step types and the JSON
// shape each one expects. Steps run sequentially against a single mock
// Cosmos and the shared envtest cluster.
package framework

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/apply_desire"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/delete_desire"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/read_desire_manager"
)

// ManagementCluster is the partition-key value used by every test case. The
// kube-applier process is single-tenant per management cluster, so a fixed
// value is correct here. After the *Desire API changed Spec.ManagementCluster
// from a free-form string to an azcorearm.ResourceID, the partition key value
// must be the lowercased canonical form of that resourceID; tests' JSON fixtures
// (e.g. artifacts/.../desire.json) likewise embed the same path string.
const ManagementCluster = "/providers/microsoft.redhatopenshift/stamps/test/managementclusters/mgmt-1"

const (
	// fastRelist is short enough that controller reactions happen within a
	// reasonable test budget; the mock cosmos answers Lists synchronously.
	fastRelist = 200 * time.Millisecond
	// EventuallyTimeout bounds how long an Eventually step polls before
	// failing. Workqueue rate-limit backoff plus cosmos relist take a few
	// seconds in the worst case under contention.
	EventuallyTimeout = 30 * time.Second
	// EventuallyTick is the polling interval for Eventually steps.
	EventuallyTick = 100 * time.Millisecond
)

// Step is the unit of work a test case runs. Each implementation parses its
// own JSON files out of stepDir and either mutates state (load/apply/delete)
// or asserts state (eventually).
type Step interface {
	// StepID returns the directory name (e.g. "01-loadApplyDesire-hello").
	// Used in test logs.
	StepID() string
	// Run executes the step against the test's harness. Failures use t.Fatal.
	Run(ctx context.Context, t *testing.T, h *Harness)
}

// Harness holds the per-test runtime that steps read from / mutate.
//
// KubeApplierDBClient is the interface (not the concrete mock) so a future
// joint backend+kube-applier test can swap in an implementation that shares
// storage with the backend's MockDBClient.
type Harness struct {
	KubeApplierDBClient database.KubeApplierDBClient
	Dyn                 dynamic.Interface
	Namespace           string
}

// TestCase is a single artifact-driven test.
type TestCase struct {
	// Name is the artifact subdirectory name.
	Name string
	// Steps are read in directory-name order.
	Steps []Step
}

// LoadTestCases discovers every test under root in artifactsFS. Each
// immediate subdirectory becomes a TestCase. Within that subdirectory, each
// numbered subdirectory becomes a Step.
func LoadTestCases(artifactsFS fs.FS, root string) ([]TestCase, error) {
	rootDir, err := fs.Sub(artifactsFS, root)
	if err != nil {
		return nil, fmt.Errorf("sub %q: %w", root, err)
	}
	entries, err := fs.ReadDir(rootDir, ".")
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", root, err)
	}

	var cases []TestCase
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		caseDir, err := fs.Sub(rootDir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("sub %q: %w", entry.Name(), err)
		}
		steps, err := loadSteps(caseDir)
		if err != nil {
			return nil, fmt.Errorf("test %q: %w", entry.Name(), err)
		}
		cases = append(cases, TestCase{Name: entry.Name(), Steps: steps})
	}
	return cases, nil
}

// loadSteps reads stepDir's immediate subdirectories, parses each name as
// "NN-stepType-description", and constructs the matching Step.
func loadSteps(testDir fs.FS) ([]Step, error) {
	entries, err := fs.ReadDir(testDir, ".")
	if err != nil {
		return nil, fmt.Errorf("read steps: %w", err)
	}
	type indexed struct {
		idx  int
		step Step
	}
	var loaded []indexed
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		parts := strings.SplitN(entry.Name(), "-", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("step %q: expected NN-stepType-description", entry.Name())
		}
		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("step %q: leading %q is not a number", entry.Name(), parts[0])
		}
		stepType := parts[1]
		stepDir, err := fs.Sub(testDir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("step %q: sub: %w", entry.Name(), err)
		}
		ctor, ok := stepConstructors[stepType]
		if !ok {
			return nil, fmt.Errorf("step %q: unknown stepType %q", entry.Name(), stepType)
		}
		step, err := ctor(entry.Name(), stepDir)
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", entry.Name(), err)
		}
		loaded = append(loaded, indexed{idx, step})
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].idx < loaded[j].idx })

	out := make([]Step, len(loaded))
	for i, l := range loaded {
		out[i] = l.step
	}
	return out, nil
}

// stepConstructors maps stepType strings to factories. Adding a new step
// type means: define newXxxStep, register it here, and document the JSON
// shape in step_xxx.go's package-level comment.
var stepConstructors = map[string]func(stepID string, stepDir fs.FS) (Step, error){
	"loadApplyDesire":      newLoadApplyDesireStep,
	"loadDeleteDesire":     newLoadDeleteDesireStep,
	"loadReadDesire":       newLoadReadDesireStep,
	"kubernetesLoad":       newKubernetesLoadStep,
	"kubernetesApply":      newKubernetesApplyStep,
	"kubernetesDelete":     newKubernetesDeleteStep,
	"desireEventually":     newDesireEventuallyStep,
	"kubernetesEventually": newKubernetesEventuallyStep,
}

// RunCase executes one TestCase against a fresh harness.
//
// It expects envtest to already be running; cfg must be its rest.Config.
// The harness creates a per-test namespace named after tc.Name (with hyphens
// preserved), so artifact JSONs that reference that exact namespace name
// will land in their own scope and not collide with sibling tests.
func (tc TestCase) RunCase(t *testing.T, cfg *rest.Config) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	dyn, err := dynamic.NewForConfig(cfg)
	require.NoError(t, err)

	namespace := strings.ReplaceAll(strings.ToLower(tc.Name), "_", "-")
	createNamespace(ctx, t, dyn, namespace)
	t.Cleanup(func() { deleteNamespace(context.Background(), t, dyn, namespace) })

	mock := databasetesting.NewMockKubeApplierDBClient()
	stop := startControllers(ctx, t, mock, dyn)
	defer stop()

	h := &Harness{KubeApplierDBClient: mock, Dyn: dyn, Namespace: namespace}

	for _, step := range tc.Steps {
		t.Logf("running step %s", step.StepID())
		step.Run(ctx, t, h)
		if t.Failed() {
			return
		}
	}
}

// startControllers wires the three kube-applier controllers in-process and
// runs them. Returns a stop function the caller defers.
func startControllers(parent context.Context, t *testing.T, kac database.KubeApplierDBClient, dyn dynamic.Interface) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)

	listers := kac.Listers()

	applyInformer := informers.NewApplyDesireInformerWithRelistDuration(listers.ApplyDesires(), fastRelist)
	deleteInformer := informers.NewDeleteDesireInformerWithRelistDuration(listers.DeleteDesires(), fastRelist)
	readInformer := informers.NewReadDesireInformerWithRelistDuration(listers.ReadDesires(), fastRelist)

	applyCtl, err := apply_desire.NewApplyDesireController(applyInformer, dyn, kac, apply_desire.Config{})
	require.NoError(t, err)
	deleteCtl, err := delete_desire.NewDeleteDesireController(deleteInformer, dyn, kac, delete_desire.Config{})
	require.NoError(t, err)
	readMgr, err := read_desire_manager.NewReadDesireInformerManagingController(readInformer, dyn, kac, read_desire_manager.Config{})
	require.NoError(t, err)

	wg := &sync.WaitGroup{}
	for _, fn := range []func(){
		func() { applyInformer.RunWithContext(ctx) },
		func() { deleteInformer.RunWithContext(ctx) },
		func() { readInformer.RunWithContext(ctx) },
	} {
		wg.Add(1)
		go func(f func()) { defer wg.Done(); f() }(fn)
	}
	syncCtx, syncCancel := context.WithTimeout(ctx, 10*time.Second)
	defer syncCancel()
	if !cache.WaitForCacheSync(syncCtx.Done(),
		applyInformer.HasSynced, deleteInformer.HasSynced, readInformer.HasSynced) {
		cancel()
		wg.Wait()
		t.Fatal("informer caches did not sync within 10s")
	}
	for _, fn := range []func(){
		func() { applyCtl.Run(ctx, 1) },
		func() { deleteCtl.Run(ctx, 1) },
		func() { readMgr.Run(ctx, 1) },
	} {
		wg.Add(1)
		go func(f func()) { defer wg.Done(); f() }(fn)
	}
	return func() { cancel(); wg.Wait() }
}

func createNamespace(ctx context.Context, t *testing.T, dyn dynamic.Interface, name string) {
	t.Helper()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	ns.SetName(name)
	_, err := dyn.Resource(gvr).Create(ctx, ns, metav1.CreateOptions{})
	require.NoErrorf(t, err, "create namespace %q", name)
}

func deleteNamespace(ctx context.Context, t *testing.T, dyn dynamic.Interface, name string) {
	t.Helper()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	_ = dyn.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
}
