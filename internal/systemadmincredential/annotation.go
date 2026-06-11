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

// Package systemadmincredential provides pure-function helpers for the
// SystemAdminCredential controllers in
// backend/pkg/controllers/systemadmincredentialcontrollers/ and for the
// frontend's OperationResult kubeconfig assembly. Nothing in this package
// performs I/O — every function is deterministic in its inputs.
//
// See docs/system-admin-credentials/PLAN.md for the design.
package systemadmincredential

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// OwnerAnnotationKey is the annotation key every kube-applier-applied k8s
// object must carry to record its ARO-HCP owner. See "Owner annotation on
// every ApplyDesire" in PLAN.md for the rationale.
const OwnerAnnotationKey = "aro-hcp.openshift.io/owner"

// setOwnerAnnotation writes the owner annotation into the given ObjectMeta.
// Every Build* helper in this package calls this on every object it
// returns. A nil owner is a programming error — the helpers panic so the
// mistake never reaches production.
func setOwnerAnnotation(meta *metav1.ObjectMeta, owner *azcorearm.ResourceID) {
	if owner == nil {
		panic("systemadmincredential: owner *azcorearm.ResourceID is required and must not be nil")
	}
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}
	meta.Annotations[OwnerAnnotationKey] = strings.ToLower(owner.String())
}
