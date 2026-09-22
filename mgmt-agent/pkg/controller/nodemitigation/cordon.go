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
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/Azure/ARO-HCP/mgmt-agent/pkg/apis/capacityreport/v1alpha1"
	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/nodehealth/detectors"
)

const cordonAnnotation = "node-mitigation.aro-hcp.azure.com/cordon-reservation"

func cordonOwner(r api.MitigationReservation) string {
	return recordName(string(r.NodeUID)) + "/" + r.ReservedAt.UTC().Format(time.RFC3339Nano)
}

func ownsCordon(node *corev1.Node, r api.MitigationReservation) bool {
	return node.UID == r.NodeUID && node.Labels[ownershipLabel] == ControllerName &&
		node.Annotations[cordonAnnotation] == cordonOwner(r)
}

func (c *Controller) checkNeverReady(node *corev1.Node) error {
	if ready(node) {
		return fmt.Errorf("node became Ready")
	}
	decision, fault := detectors.Decide(node, nil, nil, c.clock())
	if decision != detectors.DecisionWedged || fault.DetectorName != "never-ready" {
		return fmt.Errorf("node no longer meets the never-ready criterion")
	}
	if c.readiness == nil {
		return fmt.Errorf("readiness history observer is unavailable")
	}
	return c.readiness(node)
}

func (c *Controller) claimCordon(ctx context.Context, revision uint64, node *corev1.Node, r api.MitigationReservation) error {
	if node.Spec.Unschedulable || node.Labels[ownershipLabel] != "" || node.Annotations[cordonAnnotation] != "" {
		return fmt.Errorf("existing cordon or ownership cannot be adopted")
	}
	if node.UID != r.NodeUID || r.DeleteStartedAt != nil || r.ReleasedAt != nil {
		return fmt.Errorf("cordon reservation is not eligible")
	}
	labels, annotations := map[string]string{}, map[string]string{}
	maps.Copy(labels, node.Labels)
	maps.Copy(annotations, node.Annotations)
	labels[ownershipLabel] = ControllerName
	annotations[cordonAnnotation] = cordonOwner(r)
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": node.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": node.ResourceVersion},
		{"op": "add", "path": "/metadata/labels", "value": labels},
		{"op": "add", "path": "/metadata/annotations", "value": annotations},
		{"op": "add", "path": "/spec/unschedulable", "value": true},
	})
	if err != nil {
		return err
	}
	return c.write(revision, func() error {
		_, err := c.kube.CoreV1().Nodes().Patch(ctx, node.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
		return err
	})
}

// Release precedes accounting cancellation. A crash between them is recovered
// from the unowned, uncordoned Node without replaying a claim.
func (c *Controller) releaseCordon(ctx context.Context, revision uint64, r api.MitigationReservation) error {
	if r.DeleteStartedAt != nil || r.OperationToken != "" || (r.Outcome != "" && r.Outcome != "Cancelled") {
		return fmt.Errorf("attempted or uncertain deletion cannot release a cordon")
	}
	node, err := c.kube.CoreV1().Nodes().Get(ctx, r.NodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if node.UID != r.NodeUID {
		return nil
	}
	if !node.Spec.Unschedulable && node.Labels[ownershipLabel] == "" && node.Annotations[cordonAnnotation] == "" {
		return nil
	}
	if !ownsCordon(node, r) || node.Spec.ProviderID != r.ProviderID ||
		!strings.EqualFold(node.Status.NodeInfo.SystemUUID, r.InstanceID) {
		return fmt.Errorf("cordon ownership or machine identity changed; operator reconciliation required")
	}
	labels, annotations := maps.Clone(node.Labels), maps.Clone(node.Annotations)
	delete(labels, ownershipLabel)
	delete(annotations, cordonAnnotation)
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": node.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": node.ResourceVersion},
		{"op": "add", "path": "/metadata/labels", "value": labels},
		{"op": "add", "path": "/metadata/annotations", "value": annotations},
		{"op": "add", "path": "/spec/unschedulable", "value": false},
	})
	if err != nil {
		return err
	}
	return c.write(revision, func() error {
		_, err := c.kube.CoreV1().Nodes().Patch(ctx, node.Name, types.JSONPatchType, patch, metav1.PatchOptions{})
		return err
	})
}
