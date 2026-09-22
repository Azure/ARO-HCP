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

package nodehealth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const EverReadyAnnotation = "node-health.aro-hcp.azure.com/ever-ready"

const maxMachineBindings = 10000

type readinessRecord struct {
	version   string
	machine   string
	complete  bool
	everReady bool
}

// ReadinessHistory consumes the unfiltered Node watch directly. A workqueue or
// latest informer object cannot preserve intermediate Ready transitions.
type ReadinessHistory struct {
	client   kubernetes.Interface
	mu       sync.RWMutex
	records  map[types.UID]readinessRecord
	machines map[string]types.UID
	clock    func() time.Time
	since    time.Time
}

func newReadinessHistory(client kubernetes.Interface, clock func() time.Time) *ReadinessHistory {
	if clock == nil {
		clock = time.Now
	}
	return &ReadinessHistory{client: client, records: map[types.UID]readinessRecord{},
		machines: map[string]types.UID{}, clock: clock, since: clock()}
}

func (h *ReadinessHistory) invalidate() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.invalidateLocked()
}

func (h *ReadinessHistory) invalidateLocked() {
	if now := h.clock(); now.After(h.since) {
		h.since = now
	}
	for uid, record := range h.records {
		record.complete = false
		h.records[uid] = record
	}
}

func (h *ReadinessHistory) Check(node *corev1.Node, createdAt time.Time) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, found := h.records[node.UID]
	if r.everReady || node.Annotations[EverReadyAnnotation] == string(node.UID) {
		return fmt.Errorf("ready was observed for this Node UID")
	}
	machine := strings.ToLower(node.Status.NodeInfo.SystemUUID)
	if machine == "" || machine != r.machine || h.machines[machine] != node.UID {
		return fmt.Errorf("machine identity is missing, changed or registered with another Node UID")
	}
	if createdAt.IsZero() || !createdAt.After(h.since) || createdAt.After(h.clock()) ||
		node.CreationTimestamp.IsZero() || createdAt.After(node.CreationTimestamp.Time) {
		return fmt.Errorf("complete observation from Azure VM creation is unavailable")
	}
	if !found || !r.complete || r.version == "" || r.version != node.ResourceVersion {
		return fmt.Errorf("complete readiness history through the live Node version is unavailable")
	}
	return nil
}

func (h *ReadinessHistory) observe(ctx context.Context, node *corev1.Node, created, enabled bool) error {
	if node.UID == "" || node.ResourceVersion == "" {
		return fmt.Errorf("readiness observation lacks Node UID or resourceVersion")
	}
	h.mu.Lock()
	r, found := h.records[node.UID]
	if !found {
		r.complete = created && enabled
	}
	if !enabled {
		r.complete = false
	}
	r.version = node.ResourceVersion
	machine := strings.ToLower(node.Status.NodeInfo.SystemUUID)
	if r.machine != "" && r.machine != machine {
		r.complete = false
	}
	if machine != "" {
		if _, known := h.machines[machine]; !known {
			// Never evict an identity binding while its observation window can
			// still authorize deletion. A new window excludes all older VMs.
			if len(h.machines) >= maxMachineBindings {
				h.invalidateLocked()
				h.machines = map[string]types.UID{}
				r.complete = false
				klog.FromContext(ctx).Info("readiness identity limit reached; existing machine histories are unknown")
			}
			h.machines[machine] = node.UID
		}
		r.machine = machine
	}
	r.everReady = r.everReady || node.Annotations[EverReadyAnnotation] == string(node.UID)
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			r.everReady = true
		}
	}
	h.records[node.UID] = r
	h.mu.Unlock()
	if !enabled || !r.everReady || node.Annotations[EverReadyAnnotation] == string(node.UID) {
		return nil
	}
	live, err := h.client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if live.UID != node.UID {
		return fmt.Errorf("readiness marker target UID changed")
	}
	if live.Annotations[EverReadyAnnotation] == string(node.UID) {
		return nil
	}
	annotations := make(map[string]string, len(live.Annotations)+1)
	for key, value := range live.Annotations {
		annotations[key] = value
	}
	annotations[EverReadyAnnotation] = string(node.UID)
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": node.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": live.ResourceVersion},
		{"op": "add", "path": "/metadata/annotations", "value": annotations},
	})
	if err != nil {
		return err
	}
	_, err = h.client.CoreV1().Nodes().Patch(ctx, node.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
	return err
}

func (h *ReadinessHistory) forget(node *corev1.Node) {
	h.mu.Lock()
	defer h.mu.Unlock()
	machine := h.records[node.UID].machine
	if machine == "" {
		machine = strings.ToLower(node.Status.NodeInfo.SystemUUID)
	}
	if machine == "" {
		// An unidentified registration could belong to any machine.
		h.invalidateLocked()
	} else if _, known := h.machines[machine]; !known {
		h.invalidateLocked()
	}
	delete(h.records, node.UID)
}

func (h *ReadinessHistory) run(ctx context.Context, enabled func() bool) {
	defer utilruntime.HandleCrash()
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := h.session(ctx, enabled); err != nil && ctx.Err() == nil {
			klog.FromContext(ctx).Error(err, "readiness history interrupted; existing Node history is unknown")
		}
	}, time.Second)
}

func (h *ReadinessHistory) session(ctx context.Context, enabled func() bool) error {
	h.invalidate()
	defer h.invalidate()
	nodes, err := h.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	if nodes.ResourceVersion == "" {
		return fmt.Errorf("node list has no resourceVersion")
	}
	// The list snapshot can be newer than the request start. A VM born during
	// that request might already have registered, become Ready and lost its Node.
	h.invalidate()
	// Retain positive evidence after a failed marker write, but never continuity
	// across a relist. Machine bindings outlive deleted Node objects.
	present := make(map[types.UID]bool, len(nodes.Items))
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if node.UID == "" || node.ResourceVersion == "" {
			return fmt.Errorf("invalid Node identity in readiness list")
		}
		present[node.UID] = true
		if err := h.observe(ctx, node, false, enabled()); err != nil {
			klog.FromContext(ctx).Error(err, "persist everReady marker; UID remains excluded", "node", node.Name, "uid", node.UID)
		}
	}
	h.mu.Lock()
	for uid := range h.records {
		if !present[uid] {
			delete(h.records, uid)
		}
	}
	h.mu.Unlock()
	stream, err := h.client.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{ResourceVersion: nodes.ResourceVersion})
	if err != nil {
		return err
	}
	defer stream.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-stream.ResultChan():
			if !ok {
				return fmt.Errorf("node readiness watch closed")
			}
			if event.Type == watch.Error {
				return apierrors.FromObject(event.Object)
			}
			node, ok := event.Object.(*corev1.Node)
			if !ok {
				return fmt.Errorf("unexpected readiness watch object %T", event.Object)
			}
			switch event.Type {
			case watch.Added, watch.Modified:
				if node.UID == "" || node.ResourceVersion == "" {
					return fmt.Errorf("invalid Node identity in readiness watch")
				}
				if err := h.observe(ctx, node, event.Type == watch.Added, enabled()); err != nil {
					klog.FromContext(ctx).Error(err, "persist everReady marker; UID remains excluded", "node", node.Name, "uid", node.UID)
				}
			case watch.Deleted:
				h.forget(node)
			default:
				return fmt.Errorf("unexpected readiness watch event %s", event.Type)
			}
		}
	}
}
