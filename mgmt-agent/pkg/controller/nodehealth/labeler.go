// Copyright 2025 Microsoft Corporation
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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

// labeler applies and removes the node-health wedged label (plus explanatory
// annotations) on nodes, emitting Kubernetes Events and metrics. All operations
// are idempotent and non-disruptive; mitigation of a labeled node is owned by a
// separate controller.
type labeler struct {
	client   kubernetes.Interface
	recorder record.EventRecorder
	clock    func() time.Time
}

// newLabeler constructs a labeler. If clock is nil, time.Now is used.
func newLabeler(client kubernetes.Interface, recorder record.EventRecorder, clock func() time.Time) *labeler {
	if clock == nil {
		clock = time.Now
	}
	return &labeler{client: client, recorder: recorder, clock: clock}
}

// label marks a node as wedged: it sets labelKey=labelValue plus the explanatory
// annotations recording which detector fired, why, and when.
//
// It returns changed=true when it mutated the node's health record, which is
// either a fresh label transition or a refresh of a detection record that no
// longer matches the firing detector (including one whose annotations were
// stripped). A node already labeled by the same detector is left completely
// untouched, so the steady state costs no writes and no API reads.
//
// The reason annotation is deliberately not reconciled on every pass. It embeds
// live evidence counts that change constantly, so continuously reconciling it
// would rewrite every wedged node on every resync tick for no operator benefit.
// The record is a snapshot of the detection, not a live readout.
func (l *labeler) label(ctx context.Context, node *corev1.Node, detector string, snap detectors.Snapshot) (bool, error) {
	logger := klog.FromContext(ctx).WithValues("node", node.Name)

	// Steady state, off the informer cache: correct label and a matching
	// detection record. Nothing to do, and no need to hit the apiserver.
	if node.Labels[labelKey] == labelValue && node.Annotations[annotationDetector] == detector {
		logger.V(4).Info("node already labeled wedged by this detector (cache)")
		return false, nil
	}
	// Something looks like it needs writing. The informer cache lags the
	// apiserver, so a resync tick and a watch event can both reconcile off the
	// same stale view. Confirm against live state before mutating, so the counter
	// and the NodeHealthLabeled Event fire once per real change, not once per
	// racing reconcile.
	live, err := l.client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get node %s: %w", node.Name, err)
	}
	if live.Labels[labelKey] == labelValue && live.Annotations[annotationDetector] == detector {
		logger.V(4).Info("node already labeled wedged by this detector (live)")
		return false, nil
	}

	reason := snap.ReasonString()
	// An empty signature clears any annotation left by an earlier episode rather
	// than leaving a stale one next to a fresh detection record.
	var signature *string
	if snap.MatchedSignature != "" {
		signature = ptr(snap.MatchedSignature)
	}
	patch, err := metadataPatch(
		map[string]*string{labelKey: ptr(labelValue)},
		map[string]*string{
			annotationDetector:   ptr(detector),
			annotationReason:     ptr(reason),
			annotationSignature:  signature,
			annotationObservedAt: ptr(l.clock().UTC().Format(time.RFC3339)),
		},
	)
	if err != nil {
		labelActionsTotal.WithLabelValues("label", "error").Inc()
		return false, err
	}
	if err := l.patch(ctx, node.Name, patch); err != nil {
		labelActionsTotal.WithLabelValues("label", "error").Inc()
		return false, err
	}
	labelActionsTotal.WithLabelValues("label", "success").Inc()
	logger.Info("labeled node wedged", "label", labelKey+"="+labelValue, "detector", detector, "reason", reason, "signature", snap.MatchedSignature)
	l.eventf(node, corev1.EventTypeWarning, "NodeHealthLabeled",
		"node-health marked node wedged (detector %q; %s)", detector, reason)
	return true, nil
}

// unlabel removes the node-health wedged label and its annotations from a node.
// It is a no-op when the label is absent or set to a different value (so it will
// not clobber a same-key label owned by another actor). It returns changed=true
// when it applied a mutation.
func (l *labeler) unlabel(ctx context.Context, node *corev1.Node) (bool, error) {
	logger := klog.FromContext(ctx).WithValues("node", node.Name)

	if node.Labels[labelKey] != labelValue {
		return false, nil
	}
	// The informer cache lags the apiserver, so confirm the label is still
	// present at live state before mutating, so the counter and the
	// NodeHealthUnlabeled Event fire once per real transition rather than once
	// per racing reconcile off a stale still-labeled node.
	live, err := l.client.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get node %s: %w", node.Name, err)
	}
	if live.Labels[labelKey] != labelValue {
		return false, nil
	}

	patch, err := metadataPatch(
		map[string]*string{labelKey: nil},
		map[string]*string{
			annotationDetector:   nil,
			annotationReason:     nil,
			annotationSignature:  nil,
			annotationObservedAt: nil,
		},
	)
	if err != nil {
		labelActionsTotal.WithLabelValues("unlabel", "error").Inc()
		return false, err
	}
	if err := l.patch(ctx, node.Name, patch); err != nil {
		labelActionsTotal.WithLabelValues("unlabel", "error").Inc()
		return false, err
	}
	labelActionsTotal.WithLabelValues("unlabel", "success").Inc()
	logger.Info("removed wedged label from node", "label", labelKey)
	l.eventf(node, corev1.EventTypeNormal, "NodeHealthUnlabeled",
		"node-health cleared wedged label (node recovered)")
	return true, nil
}

func (l *labeler) patch(ctx context.Context, name string, patch []byte) error {
	if _, err := l.client.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch node %s: %w", name, err)
	}
	return nil
}

func (l *labeler) eventf(node *corev1.Node, eventType, reason, format string, args ...interface{}) {
	if l.recorder == nil {
		return
	}
	l.recorder.Eventf(node, eventType, reason, format, args...)
}

// metadataPatch builds a JSON merge patch (RFC 7386) for node metadata. A nil
// value removes the key; a non-nil value sets it.
func metadataPatch(labels, annotations map[string]*string) ([]byte, error) {
	meta := map[string]interface{}{}
	if len(labels) > 0 {
		meta["labels"] = nullableMap(labels)
	}
	if len(annotations) > 0 {
		meta["annotations"] = nullableMap(annotations)
	}
	b, err := json.Marshal(map[string]interface{}{"metadata": meta})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata patch: %w", err)
	}
	return b, nil
}

func nullableMap(m map[string]*string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if v == nil {
			out[k] = nil
		} else {
			out[k] = *v
		}
	}
	return out
}

func ptr(s string) *string { return &s }
