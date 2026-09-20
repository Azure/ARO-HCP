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

package nodemitigation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
	"github.com/Azure/ARO-HCP/internal/utils"
	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
	recordfake "github.com/Azure/ARO-HCP/mgmt-agent/pkg/generated/clientset/versioned/fake"
)

type fakeAzure struct {
	now         *time.Time
	target      int32
	instances   map[string]string
	instanceErr error
	poolErr     error
}

func (a *fakeAzure) Pool(_ context.Context, cluster, pool string) (PoolObservation, error) {
	return PoolObservation{ID: strings.ToLower(cluster + "/agentPools/" + pool), Target: a.target, Stable: true, ObservedAt: *a.now}, a.poolErr
}
func (a *fakeAzure) Instance(_ context.Context, _, _, provider, _ string) (string, bool, error) {
	id, exists := a.instances[provider]
	return id, exists, a.instanceErr
}

type fixture struct {
	controller *Controller
	kube       *kubefake.Clientset
	records    *recordfake.Clientset
	azure      *fakeAzure
	now        time.Time
	cfg        Config
}

func newFixture(t *testing.T, nodes, neverReady int) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), cfg: testConfig()}
	f.azure = &fakeAzure{now: &f.now, target: 10, instances: map[string]string{}}
	objects := []runtime.Object{}
	for i := 0; i < nodes; i++ {
		name := fmt.Sprintf("node-%02d", i)
		instance := fmt.Sprintf("instance-%02d", i)
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name: name, UID: types.UID(name), ResourceVersion: "1", CreationTimestamp: metav1.NewTime(f.now.Add(-time.Hour)),
			Labels: map[string]string{detectors.SwiftV2LabelKey: detectors.SwiftV2LabelValue, "kubernetes.azure.com/agentpool": "pool", corev1.LabelTopologyZone: "zone"},
		}, Spec: corev1.NodeSpec{ProviderID: "azure://" + instance}, Status: corev1.NodeStatus{
			NodeInfo:    corev1.NodeSystemInfo{SystemUUID: instance},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi"), corev1.ResourcePods: resource.MustParse("50"), kuberesources.SwiftNICResourceName: resource.MustParse("6")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(f.now.Add(-time.Hour))}},
		}}
		if i < neverReady {
			node.Status.Conditions[0].Status = corev1.ConditionFalse
		}
		objects = append(objects, node)
		f.azure.instances[node.Spec.ProviderID] = instance
	}
	f.kube = kubefake.NewClientset(objects...)
	f.records = recordfake.NewSimpleClientset()
	f.records.PrependReactor("create", "mitigationepisodes", func(action ktesting.Action) (bool, runtime.Object, error) {
		object := action.(ktesting.CreateAction).GetObject().(*api.MitigationEpisode)
		object.UID = types.UID(object.Name)
		object.ResourceVersion = "1"
		object.CreationTimestamp = metav1.NewTime(f.now)
		return false, nil, nil
	})
	eventNumber := 0
	f.kube.PrependReactor("create", "events", func(action ktesting.Action) (bool, runtime.Object, error) {
		eventNumber++
		action.(ktesting.CreateAction).GetObject().(*corev1.Event).Name = fmt.Sprintf("event-%d", eventNumber)
		return false, nil, nil
	})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{mtpncGVR: "MultitenantPodNetworkConfigList"})
	informer := informers.NewSharedInformerFactory(f.kube, 0)
	var err error
	f.controller, err = NewController(f.kube, f.records, dyn, f.azure, "mgmt-agent", informer.Core().V1().Nodes(), informer.Core().V1().Pods(), informer.Core().V1().Events(), func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	f.controller.AllowConfiguration(true)
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.controller.queue.ShutDown() })
	return f
}

func (f *fixture) tick(t *testing.T) {
	t.Helper()
	f.now = f.now.Add(2 * time.Second)
	if err := f.controller.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) episode(t *testing.T) *api.MitigationEpisode {
	t.Helper()
	result, err := f.records.MgmtagentV1alpha1().MitigationEpisodes("mgmt-agent").Get(context.Background(), episodeName("node-00"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (f *fixture) ledger(t *testing.T) *api.NodeMitigationBudget {
	t.Helper()
	result, err := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent").Get(context.Background(), budgetName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mutations(actions []ktesting.Action) []ktesting.Action {
	var result []ktesting.Action
	for _, action := range actions {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete", "delete-collection":
			result = append(result, action)
		}
	}
	return result
}

func TestAuditIndependentCandidatesNoWrites(t *testing.T) {
	f := newFixture(t, 12, 2)
	f.cfg.Mode = Audit
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	ctx := utils.ContextWithLogger(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&output, nil)))
	if err := f.controller.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), `"candidateEligible":true`) != 2 {
		t.Fatalf("expected two independent eligible candidates: %s", output.String())
	}
	if got := mutations(f.kube.Actions()); len(got) != 0 {
		t.Fatalf("audit mutated Kubernetes: %v", got)
	}
	if got := mutations(f.records.Actions()); len(got) != 0 {
		t.Fatalf("audit mutated records: %v", got)
	}
	eventLists := 0
	for _, action := range f.kube.Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "events" {
			eventLists++
		}
	}
	if eventLists != 1 {
		t.Fatalf("candidate scan listed cluster Events %d times, want one snapshot", eventLists)
	}
}

func TestFaultedReadyNodeCannotSupplyCapacity(t *testing.T) {
	f := swiftFixture(t)
	ctx := context.Background()
	snapshot, err := f.controller.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Faulted["node-00"] || snapshot.Faulted["node-01"] {
		t.Fatalf("fault evidence not correlated to the affected node: %v", snapshot.Faulted)
	}
	target, err := f.kube.CoreV1().Nodes().Get(ctx, "node-01", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.MinHealthyPool = 10
	if _, err := capacityLimits(snapshot, api.NodeMitigationBudgetStatus{}, target, f.cfg, 10); err == nil {
		t.Fatal("a Ready node with current SWIFT fault evidence supplied healthy headroom")
	}
	f.cfg.MinHealthyPool = 1
	excluded, err := capacityLimits(snapshot, api.NodeMitigationBudgetStatus{}, target, f.cfg, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !excluded["node-00"] || !excluded[target.Name] {
		t.Fatalf("faulted node remains available for placement: %v", excluded)
	}
}

func TestNeverReadyDeleteAndObserve(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	episode := f.episode(t)
	if episode.Status.NodeDeletedAt == nil || episode.Status.Phase != PhaseObserve {
		t.Fatalf("deletion not observed: %+v", episode.Status)
	}
	if _, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("original node still exists: %v", err)
	}
	reservation := f.ledger(t).Status.Reservations[episode.Name]
	if reservation.ReleasedAt != nil || reservation.DeleteStartedAt == nil {
		t.Fatal("deletion released allowance before instance removal")
	}
	f.now = f.now.Add(31 * time.Minute)
	f.tick(t)
	if !meta.IsStatusConditionTrue(f.episode(t).Status.Conditions, "InstanceCleanupStalled") {
		t.Fatal("missing 30-minute warning")
	}
	delete(f.azure.instances, "azure://instance-00")
	f.tick(t)
	if f.episode(t).Status.Phase != PhaseComplete {
		t.Fatalf("episode not complete: %+v", f.episode(t).Status)
	}
	reservation = f.ledger(t).Status.Reservations[episode.Name]
	if reservation.ReleasedAt == nil || reservation.DeleteStartedAt == nil {
		t.Fatal("completed deletion lost its rolling history")
	}
}

func TestModeChangeStopsPreparedDelete(t *testing.T) {
	for _, mode := range []Mode{Disabled, Audit} {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			for i := 0; i < 4; i++ {
				f.tick(t)
			}
			if f.episode(t).Status.Intent == nil {
				t.Fatal("test needs persisted intent")
			}
			f.cfg.Mode = mode
			if err := f.controller.SetConfig(f.cfg); err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			f.records.ClearActions()
			f.tick(t)
			if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
				t.Fatal("paused mode performed writes")
			}
			if _, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{}); err != nil {
				t.Fatal("paused mode deleted node")
			}
		})
	}
}

func TestConfigurationFence(t *testing.T) {
	f := newFixture(t, 11, 1)
	_, revision := f.controller.configuration()
	f.cfg.Mode = Audit
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := f.controller.write(revision, func() error { called = true; return nil }); !errors.Is(err, ErrPaused) || called {
		t.Fatal("stale authorization crossed write boundary")
	}
	f.controller.AllowConfiguration(false)
	f.cfg.Mode = Enforce
	if err := f.controller.SetConfig(f.cfg); err == nil {
		t.Fatal("deployment-level disable gate accepted enforce")
	}
}

func TestDeleteConflictRetainsReservation(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 4; i++ {
		f.tick(t)
	}
	f.kube.PrependReactor("delete", "nodes", func(action ktesting.Action) (bool, runtime.Object, error) {
		options := action.(ktesting.DeleteAction).GetDeleteOptions()
		if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != "node-00" ||
			options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion == "" {
			t.Fatal("DELETE lacks identity preconditions")
		}
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, "node-00", errors.New("raced"))
	})
	f.now = f.now.Add(2 * time.Second)
	if err := f.controller.reconcile(context.Background()); !apierrors.IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if f.ledger(t).Status.Reservations[f.episode(t).Name].ReleasedAt != nil {
		t.Fatal("unknown outcome expired into free allowance")
	}
}

func TestSameInstanceReregistrationCordonsNewUID(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-00", UID: "new-uid", ResourceVersion: "2"},
		Spec: corev1.NodeSpec{ProviderID: "azure://instance-00"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{SystemUUID: "instance-00"}}}
	if _, err := f.kube.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	f.tick(t)
	current, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{})
	if err != nil || current.UID != "new-uid" || !current.Spec.Unschedulable {
		t.Fatalf("new registration not safely cordoned: %+v, %v", current, err)
	}
	if f.episode(t).Status.CurrentNodeUID != "new-uid" {
		t.Fatal("registered Node UID not persisted")
	}
	if len(f.ledger(t).Status.Reservations) != 1 {
		t.Fatal("re-registration consumed another reservation")
	}
}

func TestInstanceReadErrorIsNotAbsence(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	f.azure.instanceErr = errors.New("403 forbidden")
	f.tick(t)
	episode := f.episode(t)
	if meta.IsStatusConditionTrue(episode.Status.Conditions, "InstanceGone") ||
		!meta.IsStatusConditionTrue(episode.Status.Conditions, "InstanceVerificationUnavailable") {
		t.Fatal("read failure misrepresented as absence")
	}
	if f.ledger(t).Status.Reservations[episode.Name].ReleasedAt != nil {
		t.Fatal("read failure released reservation")
	}
}

func TestRecoveredNeverReadyReleasesOnlyFencedWork(t *testing.T) {
	for _, tc := range []struct {
		name               string
		submitted, changed bool
		complete           bool
	}{
		{"unsubmitted", false, false, true},
		{"unknown outcome", true, false, false},
		{"old request fenced", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 11, 1)
			for i := 0; i < 4; i++ {
				f.tick(t)
			}
			episode := f.episode(t)
			if tc.submitted {
				at := metav1.NewTime(f.now)
				episode.Status.Intent.LastAttemptAt = &at
				if _, err := f.records.MgmtagentV1alpha1().MitigationEpisodes("mgmt-agent").UpdateStatus(context.Background(), episode, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-00", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			node.Status.Conditions[0].Status = corev1.ConditionTrue
			if tc.changed {
				node.ResourceVersion = "2"
			}
			if _, err := f.kube.CoreV1().Nodes().UpdateStatus(context.Background(), node, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			f.tick(t)
			if got := f.episode(t).Status.Phase == PhaseComplete; got != tc.complete {
				t.Fatalf("complete=%v, want %v", got, tc.complete)
			}
			if got := f.ledger(t).Status.Reservations[episode.Name].ReleasedAt != nil; got != tc.complete {
				t.Fatalf("released=%v, want %v", got, tc.complete)
			}
			for _, action := range f.kube.Actions() {
				if action.GetVerb() == "delete" {
					t.Fatal("recovered Node deleted")
				}
			}
		})
	}
}

func TestCapacityFailureDoesNotStopInstanceObservation(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 6; i++ {
		f.tick(t)
	}
	f.now = f.now.Add(31 * time.Minute)
	f.controller.dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "multitenantpodnetworkconfigs",
		func(ktesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("NIC observation unavailable")
		})
	f.kube.ClearActions()
	f.records.ClearActions()
	var output bytes.Buffer
	ctx := utils.ContextWithLogger(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&output, nil)))
	err := f.controller.reconcile(ctx)
	if err == nil || !strings.Contains(err.Error(), "NIC observation unavailable") {
		t.Fatalf("snapshot error was hidden: %v", err)
	}
	if !strings.Contains(output.String(), "InstanceCleanupStalled") {
		t.Fatal("original instance was not observed")
	}
	if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
		t.Fatal("failed capacity observation permitted writes")
	}
}

func TestDuplicateRegistrationCannotSupplyReplacementCapacity(t *testing.T) {
	f := newFixture(t, 11, 1)
	node, err := f.kube.CoreV1().Nodes().Get(context.Background(), "node-01", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	node.Name, node.UID = "duplicate", "duplicate"
	if _, err := f.kube.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	episodes, err := f.records.MgmtagentV1alpha1().MitigationEpisodes("mgmt-agent").List(context.Background(), metav1.ListOptions{})
	if err != nil || len(episodes.Items) != 0 {
		t.Fatalf("duplicate capacity was admitted: %+v, %v", episodes, err)
	}
}

func TestDelayedDeleteKeepsAFullHistoryWindow(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 4; i++ {
		f.tick(t)
	}
	f.now = f.now.Add(2 * time.Hour)
	f.tick(t)
	f.tick(t)
	delete(f.azure.instances, "azure://instance-00")
	f.tick(t)
	episode := f.episode(t)
	ledger := f.ledger(t)
	if episode.Status.Phase != PhaseComplete {
		t.Fatal("deletion was not completed")
	}
	if allowance(ledger.Status, episode.Spec.PoolID, "", f.now, f.cfg.Window.Duration) != 0 {
		t.Fatal("delayed intent aged the actual deletion out of its rolling window")
	}
	if episode.Status.NodeDeletionAction == nil || episode.Status.NodeDeletionAction.UID != episode.Spec.NodeUID {
		t.Fatal("original deletion action was lost")
	}
}

func TestPauseBeforeAbsencePersistenceKeepsWarningDeadline(t *testing.T) {
	f := newFixture(t, 11, 1)
	for i := 0; i < 5; i++ {
		f.tick(t)
	}
	if f.episode(t).Status.NodeDeletedAt != nil {
		t.Fatal("test requires absence not yet persisted")
	}
	f.cfg.Mode = Audit
	if err := f.controller.SetConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(31 * time.Minute)
	f.kube.ClearActions()
	f.records.ClearActions()
	var output bytes.Buffer
	ctx := utils.ContextWithLogger(context.Background(), logr.FromSlogHandler(slog.NewJSONHandler(&output, nil)))
	if err := f.controller.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "InstancePresentAfterDeletionAttempt") {
		t.Fatal("saved deletion attempt lost its observation deadline")
	}
	if len(mutations(f.kube.Actions())) != 0 || len(mutations(f.records.Actions())) != 0 {
		t.Fatal("paused observation wrote state")
	}
}

func TestMissingAccountingCannotCreateFreeAllowance(t *testing.T) {
	for _, entireLedger := range []bool{false, true} {
		t.Run(fmt.Sprintf("entire-ledger-%t", entireLedger), func(t *testing.T) {
			f := newFixture(t, 11, 1)
			for i := 0; i < 4; i++ {
				f.tick(t)
			}
			ctx := context.Background()
			ledger := f.ledger(t)
			if entireLedger {
				if err := f.records.Tracker().Delete(api.SchemeGroupVersion.WithResource("nodemitigationbudgets"), "mgmt-agent", budgetName); err != nil {
					t.Fatal(err)
				}
			} else {
				clear(ledger.Status.Reservations)
				if _, err := f.records.MgmtagentV1alpha1().NodeMitigationBudgets("mgmt-agent").UpdateStatus(ctx, ledger, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}

			f.records.ClearActions()
			f.kube.ClearActions()
			err := f.controller.reconcile(ctx)
			if entireLedger && (err == nil || !strings.Contains(err.Error(), "accounting is missing")) {
				t.Fatalf("missing ledger not reported: %v", err)
			}
			if !entireLedger && err != nil {
				t.Fatal(err)
			}
			for _, action := range append(f.kube.Actions(), f.records.Actions()...) {
				if action.GetVerb() == "delete" || action.GetVerb() == "create" {
					t.Fatalf("lost accounting permitted %v", action)
				}
			}
			if f.episode(t).Status.Phase == PhaseComplete {
				t.Fatal("unknown work was completed")
			}
		})
	}
}
