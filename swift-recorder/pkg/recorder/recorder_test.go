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

package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/discovery"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/testutil"
)

type logSink struct {
	records *[]map[string]any
	values  []any
	notify  chan struct{}
}

func (s logSink) Init(logr.RuntimeInfo) {}
func (s logSink) Enabled(int) bool      { return true }
func (s logSink) Info(_ int, _ string, values ...any) {
	for i := 0; i+1 < len(values); i += 2 {
		if values[i] == "record" {
			var record map[string]any
			if raw, ok := values[i+1].(json.RawMessage); ok && json.Unmarshal(raw, &record) == nil {
				for j := 0; j+1 < len(s.values); j += 2 {
					record[s.values[j].(string)] = s.values[j+1]
				}
				*s.records = append(*s.records, record)
				select {
				case s.notify <- struct{}{}:
				default:
				}
			}
		}
	}
}
func (s logSink) Error(error, string, ...any) {}
func (s logSink) WithValues(values ...any) logr.LogSink {
	s.values = append(append([]any(nil), s.values...), values...)
	return s
}
func (s logSink) WithName(string) logr.LogSink { return s }

type fixture struct {
	c       *Controller
	now     time.Time
	calls   int
	result  json.RawMessage
	err     error
	records []map[string]any
	ctx     context.Context
	t       *testing.T
	client  *fake.Clientset
}

func newFixture(t *testing.T, mode string) *fixture {
	t.Helper()
	f := &fixture{t: t, now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), result: json.RawMessage(`{"links":[]}`)}
	f.client = fake.NewClientset()
	pods := informers.NewSharedInformerFactory(f.client, 0).Core().V1().Pods()
	cfg := Config{NodeName: "node", ClusterName: "cluster", BootID: "boot", NetNSDir: testutil.ResolvedTempDir(t), Executable: "helper", CaptureMode: mode,
		StartupDwell: 10 * time.Second, PostSuccessCapture: 5 * time.Second, SampleInterval: time.Second, CaptureTimeout: time.Second, EpisodeTimeout: time.Minute,
		MaxPods: 2, MaxRecordBytes: 2048, MaxBufferBytes: 8192}
	var err error
	f.c, err = New(cfg, pods, func(ctx context.Context, executable, path string, limit int) (json.RawMessage, error) {
		f.calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("capture must have a deadline")
		}
		if executable != "helper" || filepath.Dir(path) != cfg.NetNSDir || limit > cfg.MaxRecordBytes {
			t.Error("unexpected capture parameters")
		}
		return f.result, f.err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.c.now = func() time.Time { return f.now }
	f.ctx = utils.ContextWithLogger(context.Background(), logr.New(logSink{records: &f.records}))
	t.Cleanup(f.c.queue.ShutDown)
	return f
}

func (f *fixture) pod(uid string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: uid, UID: types.UID(uid)},
		Spec: corev1.PodSpec{NodeName: "node", Containers: []corev1.Container{{Name: "main", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{swiftResource: resource.MustParse("1")}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(f.now)},
			{Type: corev1.PodReadyToStartContainers, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(f.now)},
		}}}
}

func (f *fixture) put(pod *corev1.Pod) {
	f.t.Helper()
	old, exists, err := f.c.pods.Informer().GetStore().Get(pod)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.c.pods.Informer().GetStore().Update(pod); err != nil {
		f.t.Fatal(err)
	}
	if !exists {
		old = nil
	}
	f.c.observePod(old, pod)
}

func (f *fixture) attempt(pod *corev1.Pod, sandbox string) {
	f.c.ObserveAttempt(discovery.Attempt{PodUID: string(pod.UID), Namespace: pod.Namespace, PodName: pod.Name, SandboxID: sandbox,
		NetNS: filepath.Join(f.c.cfg.NetNSDir, "cni-"+sandbox), ObservedAt: f.now})
}

func (f *fixture) sync(pod *corev1.Pod)    { f.c.sync(f.ctx, podKey(pod)) }
func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }
func (f *fixture) recover(pod *corev1.Pod) *corev1.Pod {
	copy := pod.DeepCopy()
	copy.Status.Conditions[1].Status = corev1.ConditionTrue
	copy.Status.Conditions[1].LastTransitionTime = metav1.NewTime(f.now)
	f.put(copy)
	return copy
}
func (f *fixture) events() string {
	var events []string
	for _, r := range f.records {
		events = append(events, r["event"].(string))
	}
	return strings.Join(events, ",")
}

func TestEligibility(t *testing.T) {
	f := newFixture(t, "slow")
	for _, tc := range []struct {
		name   string
		change func(*corev1.Pod)
		want   bool
	}{
		{"requested", func(*corev1.Pod) {}, true},
		{"other_node", func(p *corev1.Pod) { p.Spec.NodeName = "other" }, false},
		{"host_network", func(p *corev1.Pod) { p.Spec.HostNetwork = true }, false},
		{"terminal", func(p *corev1.Pod) { p.Status.Phase = corev1.PodSucceeded }, false},
		{"zero", func(p *corev1.Pod) { p.Spec.Containers[0].Resources.Requests[swiftResource] = resource.MustParse("0") }, false},
		{"negative", func(p *corev1.Pod) { p.Spec.Containers[0].Resources.Requests[swiftResource] = resource.MustParse("-1") }, false},
		{"init_limit", func(p *corev1.Pod) {
			p.Spec.InitContainers = p.Spec.Containers
			p.Spec.Containers = nil
			p.Spec.InitContainers[0].Resources.Limits = p.Spec.InitContainers[0].Resources.Requests
			p.Spec.InitContainers[0].Resources.Requests = nil
		}, true},
		{"second_container", func(p *corev1.Pod) {
			p.Spec.Containers = append([]corev1.Container{{Name: "first"}}, p.Spec.Containers...)
		}, true},
		{"absent_condition", func(p *corev1.Pod) { p.Status.Conditions = nil }, true},
		{"old_running", func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodRunning
			p.Status.StartTime = &metav1.Time{Time: f.now.Add(-time.Hour)}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pod := f.pod(tc.name)
			tc.change(pod)
			if got := f.c.candidate(pod, f.now); got != tc.want {
				t.Fatalf("candidate=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestSlowTimeline(t *testing.T) {
	f := newFixture(t, "slow")
	p := f.pod("pod")
	f.attempt(p, "one") // ADD preceding the pod watch must survive a NotFound reconcile.
	f.sync(p)
	f.put(p)
	f.sync(p)
	if f.calls != 1 || len(f.records) != 0 {
		t.Fatal("startup must capture but buffer before dwell")
	}
	f.advance(9 * time.Second)
	f.sync(p)
	if len(f.records) != 0 {
		t.Fatal("published before dwell")
	}
	f.advance(time.Second)
	f.sync(p)
	if f.events() != "open,snapshot" {
		t.Fatalf("dwell flush/dedup: %s", f.events())
	}
	f.advance(time.Second)
	p = f.recover(p)
	f.result = json.RawMessage(`{"links":[1]}`)
	f.sync(p)
	if f.events() != "open,snapshot,recovery,snapshot" {
		t.Fatalf("recovery timeline: %s", f.events())
	}
	f.advance(time.Second)
	f.sync(p)
	f.advance(4 * time.Second)
	f.sync(p)
	if f.events() != "open,snapshot,recovery,snapshot,close" || len(f.c.episodes) != 0 || f.c.bufferBytes != 0 {
		t.Fatalf("close timeline: %s", f.events())
	}
	f.attempt(p, "restart")
	f.sync(p)
	if len(f.c.episodes) != 0 {
		t.Fatal("completed pod reopened")
	}
}

func TestHealthyBeforeDwell(t *testing.T) {
	for _, recovery := range []time.Duration{5 * time.Second, 10 * time.Second} {
		t.Run(recovery.String(), func(t *testing.T) {
			f := newFixture(t, "slow")
			p := f.pod("pod")
			f.put(p)
			f.attempt(p, "one")
			f.sync(p)
			f.advance(recovery)
			p = f.recover(p)
			f.sync(p)
			if len(f.records) != 0 || f.c.bufferBytes != 0 || len(f.c.episodes) != 0 {
				t.Fatal("healthy at/before dwell must discard")
			}
		})
	}
}

func TestUnknownStartupDwell(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			f := newFixture(t, "slow")
			p := f.pod("unknown")
			p.Status.Conditions[1].Status = corev1.ConditionUnknown
			if missing {
				p.Status.Conditions = nil
			}
			f.put(p)
			f.attempt(p, "one")
			f.sync(p)
			if len(f.records) != 0 {
				t.Fatal("unknown startup published before dwell")
			}
			f.advance(f.c.cfg.StartupDwell)
			f.sync(p)
			if f.events() != "open,snapshot" {
				t.Fatalf("unknown startup did not publish at dwell: %s", f.events())
			}
			for _, record := range f.records {
				if record["network_condition"] != string(corev1.ConditionUnknown) {
					t.Fatal("unknown condition must not be labeled as a network failure")
				}
			}
			f.advance(f.c.cfg.EpisodeTimeout)
			f.sync(p)
			if f.c.bufferBytes != 0 || len(f.c.episodes) != 0 {
				t.Fatal("unknown episode must expire")
			}
		})
	}
}

func TestAllLateRunningWatch(t *testing.T) {
	for _, attemptFirst := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("attemptFirst=%v/missing=%v", attemptFirst, missing), func(t *testing.T) {
				f := newFixture(t, "all")
				p := f.pod("pod")
				p.Status.StartTime = &metav1.Time{Time: f.now.Add(-30 * time.Second)}
				p.Status.Conditions[0].LastTransitionTime = *p.Status.StartTime
				if attemptFirst {
					f.attempt(p, "one")
					f.sync(p) // ADD may arrive before the pod is visible.
				}
				p.Status.Phase = corev1.PodRunning
				p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(f.now)}}}}
				p.Status.Conditions[1].Status = corev1.ConditionTrue
				if missing {
					p.Status.Conditions = p.Status.Conditions[:1]
				}
				f.advance(time.Second)
				f.put(p)
				if !attemptFirst {
					f.attempt(p, "one")
				}
				f.sync(p)
				if f.calls != 1 || f.events() != "open,recovery,snapshot" {
					t.Fatalf("lost fast startup with old scheduling time: %s (%d captures)", f.events(), f.calls)
				}
				f.advance(4 * time.Second)
				f.sync(p)
				if len(f.c.episodes) != 0 {
					t.Fatal("late observation extended the post-success window")
				}
			})
		}
	}
}

func TestStalledStartupCannotReopen(t *testing.T) {
	f := newFixture(t, "all")
	f.c.cfg.EpisodeTimeout = 15 * time.Minute
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	f.advance(f.c.cfg.EpisodeTimeout)
	f.sync(p)
	f.advance(max(identityTTL, f.c.cfg.EpisodeTimeout))
	f.attempt(p, "retry")
	f.sync(p)
	if len(f.c.completed) != 0 || len(f.c.episodes) != 0 || len(f.c.observed) != 0 || len(f.c.pending) != 0 {
		t.Fatal("stalled pod reopened after completion suppression expired")
	}
	// A new recorder has no completion history, so admission must also use pod age.
	f.c.observePod(nil, p)
	f.sync(p)
	if len(f.c.episodes) != 0 || f.c.candidate(p, f.now) {
		t.Fatal("recorder restart readmitted old stalled pod")
	}
}

func TestAttemptSurvivesPendingExpiry(t *testing.T) {
	f := newFixture(t, "all")
	f.c.cfg.EpisodeTimeout = 15 * time.Minute
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	f.advance(identityTTL)
	f.result = json.RawMessage(`{"links":[1]}`)
	f.sync(p)
	if len(f.c.pending) != 0 || f.calls != 2 || f.c.episodes[podKey(p)].attempt.SandboxID != "one" {
		t.Fatal("pending expiry lost active episode identity")
	}
}

func TestIneligibleAttemptsDoNotConsumeCapacity(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		for _, order := range []string{"pod_first", "attempt_first", "worker"} {
			t.Run(fmt.Sprintf("healthy=%v/%s", healthy, order), func(t *testing.T) {
				f := newFixture(t, "slow")
				for i := 0; i < identityLimit+10; i++ {
					p := f.pod(fmt.Sprint(i))
					if healthy {
						p.Status.Conditions[1].Status = corev1.ConditionTrue
					} else {
						p.Spec.Containers[0].Resources.Requests = nil
					}
					if order != "pod_first" {
						f.attempt(p, "ignored")
					}
					if order == "worker" {
						if err := f.c.pods.Informer().GetStore().Add(p); err != nil {
							t.Fatal(err)
						}
						f.sync(p)
					} else {
						f.put(p)
					}
					if order == "pod_first" {
						f.attempt(p, "ignored")
					}
				}
				if len(f.c.pending) != 0 || len(f.c.observed) != 0 || f.c.queue.Len() != 0 {
					t.Fatal("known non-candidates consumed ingress or queue capacity")
				}
				p := f.pod("eligible")
				f.put(p)
				f.attempt(p, "one")
				f.sync(p)
				if f.calls != 1 || f.c.episodes[podKey(p)].attempt.SandboxID != "one" {
					t.Fatal("ignored ADDs starved eligible startup")
				}
			})
		}
	}
}

func TestUnknownAttemptsDoNotQueueAcrossExpiry(t *testing.T) {
	f := newFixture(t, "all")
	for round := 0; round < 3; round++ {
		for i := 0; i < identityLimit+10; i++ {
			p := f.pod(fmt.Sprintf("%d-%d", round, i))
			f.attempt(p, "unknown")
			f.sync(p)
		}
		if len(f.c.pending) != identityLimit || f.c.queue.Len() != 0 || len(f.c.observed) != 0 {
			t.Fatal("unknown identities must be bounded without queueing")
		}
		f.advance(identityTTL)
	}
	f.c.expire(f.now)
	if len(f.c.pending) != 0 {
		t.Fatal("unknown identities did not expire")
	}
}

func TestAttemptInformerRace(t *testing.T) {
	for _, order := range []string{"attempt_first", "store_first", "callback_first"} {
		t.Run(order, func(t *testing.T) {
			f := newFixture(t, "all")
			p := f.pod("pod")
			switch order {
			case "attempt_first":
				f.attempt(p, "one")
				f.sync(p)
				if len(f.c.pending) != 1 || f.c.queue.Len() != 0 {
					t.Fatal("unknown ADD must survive without queueing")
				}
				f.put(p)
			case "store_first":
				if err := f.c.pods.Informer().GetStore().Add(p); err != nil {
					t.Fatal(err)
				}
				f.attempt(p, "one")
				f.c.observePod(nil, p)
			case "callback_first":
				f.put(p)
				f.attempt(p, "one")
			}
			if f.c.queue.Len() != 1 || len(f.c.observed) != 1 {
				t.Fatal("visible candidate must hold exactly one admission and queue slot")
			}
			f.c.processNext(f.ctx)
			if f.calls != 1 || f.c.episodes[podKey(p)].attempt.SandboxID != "one" {
				t.Fatal("informer/ADD ordering lost startup identity")
			}
		})
	}
}

func TestQueuedCandidatesRemainBoundedAcrossExpiry(t *testing.T) {
	for _, withAttempt := range []bool{false, true} {
		t.Run(fmt.Sprint(withAttempt), func(t *testing.T) {
			f := newFixture(t, "all")
			for round := 0; round < 3; round++ {
				for i := 0; i < identityLimit+10; i++ {
					p := f.pod(fmt.Sprintf("%d-%d", round, i))
					f.put(p)
					f.put(p.DeepCopy())
					if withAttempt {
						f.attempt(p, "one")
					}
				}
				if len(f.c.observed) != f.c.cfg.MaxPods || f.c.queue.Len() != f.c.cfg.MaxPods || len(f.c.pending) > f.c.cfg.MaxPods {
					t.Fatal("ingress expiry released slots before queued candidates were processed")
				}
				f.advance(identityTTL)
			}
		})
	}
}

func TestBufferRetainsRecentEvidence(t *testing.T) {
	f := newFixture(t, "slow")
	f.c.cfg.MaxBufferBytes = f.c.cfg.MaxRecordBytes * f.c.cfg.MaxPods
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	for i := 0; i < 10; i++ {
		f.result = json.RawMessage(fmt.Sprintf(`{"sample":%d,"padding":"%s"}`, i, strings.Repeat("x", 500)))
		f.sync(p)
		f.advance(time.Second)
	}
	e := f.c.episodes[podKey(p)]
	if e.bytes > f.c.cfg.MaxBufferBytes/f.c.cfg.MaxPods || e.bytes != f.c.bufferBytes {
		t.Fatal("rolling buffer exceeded budget or lost byte accounting")
	}
	f.sync(p)
	last := f.records[len(f.records)-1]["snapshot"].(map[string]any)["state"].(map[string]any)
	if last["sample"] != float64(9) || f.c.bufferBytes != 0 {
		t.Fatal("dwell publication did not retain latest buffered evidence")
	}
}

func TestAllFastRecoveryBeforeWorker(t *testing.T) {
	f := newFixture(t, "all")
	p := f.pod("pod")
	f.attempt(p, "one")
	f.put(p)
	f.advance(time.Second)
	p = f.recover(p)
	f.sync(p)
	if f.calls != 1 || f.events() != "open,recovery,snapshot" {
		t.Fatalf("lost fast startup: %s (%d captures)", f.events(), f.calls)
	}
	f.advance(5 * time.Second)
	f.sync(p)
	if len(f.c.episodes) != 0 {
		t.Fatal("all episode did not close")
	}
}

func TestScheduledDwellAndNilTransition(t *testing.T) {
	for _, oldSchedule := range []bool{false, true} {
		t.Run(fmt.Sprint(oldSchedule), func(t *testing.T) {
			f := newFixture(t, "slow")
			p := f.pod("pod")
			p.Status.Conditions[1].LastTransitionTime = metav1.Time{}
			if oldSchedule {
				p.Status.Conditions[0].LastTransitionTime = metav1.NewTime(f.now.Add(-30 * time.Second))
			}
			f.put(p)
			f.attempt(p, "one")
			f.sync(p)
			if (len(f.records) > 0) != oldSchedule {
				t.Fatal("dwell must use scheduling time, not ready condition transition")
			}
			if !oldSchedule {
				f.advance(10 * time.Second)
				f.sync(p)
				if len(f.records) == 0 {
					t.Fatal("nil ready transition must still reach dwell")
				}
			}
		})
	}
}

func TestBoundsAndRetries(t *testing.T) {
	f := newFixture(t, "slow")
	f.err = errors.New("namespace missing: secret helper stderr")
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	q := f.pod("other")
	f.put(q)
	f.attempt(q, "two")
	f.sync(q)
	f.put(f.pod("overflow"))
	if len(f.c.observed) != 2 {
		t.Fatal("pod admission limit exceeded")
	}
	f.c.cfg.StartupDwell = time.Hour // Keep subsequent evidence buffered until the fixed episode timeout.
	for _, e := range f.c.episodes {
		e.dwell = f.now.Add(time.Hour)
	}
	for i := 0; i < 59; i++ {
		f.advance(time.Second)
		f.sync(p)
		f.sync(q)
		if f.c.bufferBytes > f.c.cfg.MaxBufferBytes {
			t.Fatal("global budget exceeded")
		}
		for _, e := range f.c.episodes {
			if e.bytes > f.c.cfg.MaxBufferBytes/f.c.cfg.MaxPods {
				t.Fatal("per-pod fair budget exceeded")
			}
			for _, r := range e.buffer {
				if len(r) > f.c.cfg.MaxRecordBytes || strings.Contains(string(r), "secret") {
					t.Fatal("record not bounded/sanitized")
				}
			}
		}
	}
	f.advance(time.Second)
	f.sync(p)
	f.sync(q)
	calls := f.calls
	f.advance(time.Minute)
	f.sync(p)
	if f.calls != calls || len(f.c.episodes) != 0 || f.c.bufferBytes != 0 || f.c.queue.NumRequeues(podKey(p)) != 0 {
		t.Fatal("capture retries did not terminate and release memory")
	}
	for i := 0; i < 100; i++ {
		r := f.pod(fmt.Sprint(i))
		f.attempt(r, "pending")
	}
	if len(f.c.pending) != identityLimit {
		t.Fatal("pending attempts must be bounded")
	}
	f.advance(identityTTL)
	f.attempt(f.pod("fresh"), "fresh")
	if len(f.c.pending) != 1 {
		t.Fatal("pending attempt TTL not enforced")
	}
}

func TestDeletionStaleUIDAndNamespaceReuse(t *testing.T) {
	f := newFixture(t, "all")
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	f.advance(time.Second)
	f.attempt(p, "two")
	f.sync(p)
	if f.events() != "open,snapshot,snapshot" {
		t.Fatal("same state in new sandbox must not be deduplicated")
	}
	if err := f.c.pods.Informer().GetStore().Delete(p); err != nil {
		t.Fatal(err)
	}
	f.sync(p)
	if len(f.c.episodes) != 0 || f.c.bufferBytes != 0 {
		t.Fatal("deletion leaked episode")
	}
	newPod := f.pod("new-uid")
	newPod.Name = p.Name
	f.put(newPod)
	f.c.ObserveAttempt(discovery.Attempt{PodUID: string(p.UID), Namespace: p.Namespace, PodName: p.Name, NetNS: filepath.Join(f.c.cfg.NetNSDir, "cni-old")})
	calls := f.calls
	f.sync(p)
	if f.calls != calls {
		t.Fatal("stale UID captured replacement pod")
	}
	f.attempt(newPod, "one")
	f.sync(newPod)
	if f.calls != calls+1 {
		t.Fatal("new UID should have its own episode")
	}
}

func TestNamespaceValidation(t *testing.T) {
	f := newFixture(t, "all")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(f.c.cfg.NetNSDir, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "relative/cni-x", "/outside/cni-x", filepath.Join(f.c.cfg.NetNSDir, "not-cni"), f.c.cfg.NetNSDir + "/../cni-x", filepath.Join(alias, "cni-x")} {
		if _, err := f.c.namespacePath(path); err == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	path := filepath.Join(f.c.cfg.NetNSDir, "cni-missing")
	if got, err := f.c.namespacePath(path); err != nil || got != path {
		t.Fatal("missing leaf must reach helper for bounded retry", got, err)
	}
}

func TestCaptureValidationAndRecoveryAfterError(t *testing.T) {
	f := newFixture(t, "all")
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	f.advance(time.Second)
	f.err = errors.New("missing namespace")
	f.sync(p)
	f.advance(time.Second)
	f.err = nil
	f.sync(p)
	if f.events() != "open,snapshot,error,snapshot" {
		t.Fatalf("error must break consecutive dedup: %s", f.events())
	}
	for _, data := range []json.RawMessage{json.RawMessage(`invalid`), json.RawMessage(`"` + strings.Repeat("x", f.c.cfg.MaxRecordBytes) + `"`)} {
		f.advance(time.Second)
		f.result = data
		f.sync(p)
		if f.records[len(f.records)-1]["event"] != "error" {
			t.Fatal("invalid capture must become an error")
		}
	}
	f.advance(time.Second)
	f.result = json.RawMessage(`"` + strings.Repeat("x", f.c.cfg.MaxRecordBytes-2) + `"`)
	count := len(f.records)
	f.sync(p)
	if len(f.records) != count {
		t.Fatal("full record envelope must obey max record bytes")
	}
}

func TestCaptureErrorReasons(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", fmt.Errorf("secret: %w", os.ErrPermission), "permission_denied"},
		{"missing", fmt.Errorf("secret: %w", os.ErrNotExist), "not_found"},
		{"timeout", fmt.Errorf("secret: %w", context.DeadlineExceeded), "capture_timeout"},
		{"helper", fmt.Errorf("secret: %w", &exec.ExitError{}), "helper_failed"},
		{"unknown", errors.New("secret"), "capture_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "all")
			p := f.pod("pod")
			f.put(p)
			f.attempt(p, "one")
			f.err = tc.err
			f.sync(p)
			last := f.records[len(f.records)-1]
			if last["event"] != "error" || last["reason"] != tc.want {
				t.Fatalf("unexpected capture error: %v", last)
			}
			if strings.Contains(fmt.Sprint(f.records), "secret") {
				t.Fatal("raw capture error leaked")
			}
		})
	}
}

func TestRecoveryRegressionDoesNotExtendEpisode(t *testing.T) {
	f := newFixture(t, "all")
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	f.advance(time.Second)
	p = f.recover(p)
	f.sync(p)
	f.advance(time.Second)
	p = p.DeepCopy()
	p.Status.Conditions[1].Status = corev1.ConditionFalse
	f.put(p)
	f.attempt(p, "restart")
	f.sync(p)
	f.advance(4 * time.Second)
	f.sync(p)
	if len(f.c.episodes) != 0 {
		t.Fatal("readiness regression extended the post-success window")
	}
}

func TestCompletionCacheBoundAndExpiry(t *testing.T) {
	f := newFixture(t, "all")
	for i := 0; i < identityLimit+10; i++ {
		p := f.pod(fmt.Sprint(i))
		f.put(p)
		f.c.finish(f.ctx, podKey(p), "deleted_or_ineligible")
	}
	if len(f.c.completed) != identityLimit || len(f.c.observed) != 0 {
		t.Fatal("completion suppression must have a fixed bound")
	}
	f.advance(identityTTL)
	f.c.mu.Lock()
	f.c.expire(f.now)
	f.c.mu.Unlock()
	if len(f.c.completed) != 0 {
		t.Fatal("completion suppression must expire")
	}
}

func TestDisappearedNamespaceClosesAtDeadline(t *testing.T) {
	f := newFixture(t, "all")
	p := f.pod("pod")
	f.put(p)
	f.attempt(p, "one")
	f.sync(p)
	// The helper has returned and released its namespace references. Losing the
	// path must not retain the episode indefinitely or expose helper stderr.
	f.err = fmt.Errorf("namespace disappeared: %w", &exec.ExitError{})
	f.advance(time.Second)
	f.sync(p)
	if f.records[len(f.records)-1]["reason"] != "helper_failed" {
		t.Fatal("namespace disappearance must be reported through the helper")
	}
	f.advance(f.c.cfg.EpisodeTimeout)
	f.sync(p)
	calls := f.calls
	f.advance(time.Second)
	f.sync(p)
	if f.records[len(f.records)-1]["reason"] != "timeout" || f.calls != calls || len(f.c.episodes) != 0 || len(f.c.pending) != 0 || len(f.c.observed) != 0 || f.c.bufferBytes != 0 {
		t.Fatal("disappeared namespace retained episode state past its deadline")
	}
}

func TestRunCancellationDuringCapture(t *testing.T) {
	f := newFixture(t, "slow")
	f.c.now = time.Now
	f.now = time.Now()
	p := f.pod("pod")
	f.attempt(p, "one")
	if _, err := f.client.CoreV1().Pods(p.Namespace).Create(f.ctx, p, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	entered, exited := make(chan struct{}), make(chan struct{})
	f.c.captureFn = func(ctx context.Context, _, _ string, _ int) (json.RawMessage, error) {
		close(entered)
		defer close(exited)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	go f.c.pods.Informer().Run(ctx.Done())
	go func() { done <- f.c.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start capture")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop capture and worker")
	}
	select {
	case <-exited:
	default:
		t.Fatal("Run returned before capture released its resources")
	}
	f.attempt(p, "after_shutdown")
	f.c.observePod(nil, p)
	if f.c.Ready() || len(f.c.episodes) != 0 || len(f.c.observed) != 0 || len(f.c.pending) != 0 || len(f.c.completed) != 0 || f.c.bufferBytes != 0 || len(f.records) != 0 {
		t.Fatal("cancellation retained ingress or buffered evidence")
	}
}

func TestRunReadinessAndMetrics(t *testing.T) {
	f := newFixture(t, "all")
	// Use the real clock when callbacks and worker run concurrently.
	f.c.now = time.Now
	f.now = time.Now()
	if _, err := f.client.CoreV1().Pods("ns").Create(f.ctx, f.pod("pod"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	notified := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(utils.ContextWithLogger(context.Background(), logr.New(logSink{records: &f.records, notify: notified})))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.c.Run(ctx) }()
	if f.c.Ready() {
		t.Fatal("ready before informer sync")
	}
	go f.c.pods.Informer().Run(ctx.Done())
	deadline := time.After(5 * time.Second)
	for !f.c.Ready() {
		select {
		case err := <-done:
			t.Fatalf("Run exited early: %v", err)
		case <-deadline:
			t.Fatal("controller never ready")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not emit startup records")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run failed to stop")
	}
	if f.c.Ready() {
		t.Fatal("ready after shutdown")
	}
	if len(f.records) == 0 {
		t.Fatal("Run did not emit startup records")
	}
	for _, record := range f.records {
		if record["capture_mode"] != "all" || record["controller_name"] != RecorderControllerName {
			t.Fatalf("missing recorder log context: %v", record)
		}
	}
	if err := f.c.Run(ctx); err == nil {
		t.Fatal("second Run must fail")
	}
	episodes.WithLabelValues("started").Add(0)
	captures.WithLabelValues("success").Add(0)
	overflows.WithLabelValues("record").Add(0)
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	queueFound := false
	for _, family := range families {
		if family.GetName() == "workqueue_depth" {
			for _, metric := range family.Metric {
				for _, label := range metric.Label {
					queueFound = queueFound || (label.GetName() == "name" && label.GetValue() == RecorderControllerName)
				}
			}
		}
		if !strings.HasPrefix(family.GetName(), "swift_recorder_") {
			continue
		}
		found++
		for _, metric := range family.Metric {
			if len(metric.Label) != 1 || (metric.Label[0].GetName() != "outcome" && metric.Label[0].GetName() != "limit") {
				t.Fatal("unbounded metric label")
			}
		}
	}
	if found != 3 {
		t.Fatalf("expected three registered recorder counters, got %d", found)
	}
	if !queueFound {
		t.Fatal("named workqueue metrics were not registered")
	}
}
