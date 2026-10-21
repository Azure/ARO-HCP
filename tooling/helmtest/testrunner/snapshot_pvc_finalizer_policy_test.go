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

package testrunner

import (
	"os"
	"testing"
	"time"

	"github.com/google/cel-go/cel"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/yaml"
)

func TestSnapshotPVCFinalizerPolicy(t *testing.T) {
	raw, err := os.ReadFile("../../../mgmt-agent/deploy/templates/snapshot-pvc-finalizer-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var policy admissionregistrationv1.ValidatingAdmissionPolicy
	if err := yaml.UnmarshalStrict(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Spec.FailurePolicy == nil || *policy.Spec.FailurePolicy != admissionregistrationv1.Fail ||
		len(policy.Spec.MatchConditions) != 1 ||
		policy.Spec.MatchConstraints == nil || len(policy.Spec.MatchConstraints.ResourceRules) != 1 {
		t.Fatal("policy must fail closed and match only the mgmt-agent service account's PVC updates")
	}
	if policy.Spec.MatchConditions[0].Expression !=
		"request.userInfo.username == 'system:serviceaccount:{{ .Release.Namespace }}:{{ .Values.serviceAccount.name }}'" {
		t.Fatalf("unexpected identity condition: %q", policy.Spec.MatchConditions[0].Expression)
	}
	rule := policy.Spec.MatchConstraints.ResourceRules[0]
	if len(rule.APIGroups) != 1 || rule.APIGroups[0] != "" ||
		len(rule.APIVersions) != 1 || rule.APIVersions[0] != "v1" ||
		len(rule.Operations) != 1 || rule.Operations[0] != admissionregistrationv1.Update ||
		len(rule.Resources) != 1 || rule.Resources[0] != "persistentvolumeclaims" ||
		rule.Scope == nil || *rule.Scope != admissionregistrationv1.NamespacedScope {
		t.Fatalf("unexpected resource rule: %+v", rule)
	}
	bindingRaw, err := os.ReadFile("../../../mgmt-agent/deploy/templates/snapshot-pvc-finalizer-policy-binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var binding admissionregistrationv1.ValidatingAdmissionPolicyBinding
	if err := yaml.UnmarshalStrict(bindingRaw, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.Spec.PolicyName != policy.Name ||
		len(binding.Spec.ValidationActions) != 1 || binding.Spec.ValidationActions[0] != admissionregistrationv1.Deny {
		t.Fatalf("binding must deny policy violations: %+v", binding.Spec)
	}
	if len(policy.Spec.Validations) != 1 {
		t.Fatalf("expected one validation, got %d", len(policy.Spec.Validations))
	}

	env, err := cel.NewEnv(
		cel.OptionalTypes(),
		cel.Variable("oldObject", cel.DynType),
		cel.Variable("object", cel.DynType),
	)
	if err != nil {
		t.Fatal(err)
	}
	ast, issues := env.Compile(policy.Spec.Validations[0].Expression)
	if issues != nil && issues.Err() != nil {
		t.Fatal(issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		t.Fatal(err)
	}

	const snapshotFinalizer = "snapshot.storage.kubernetes.io/pvc-as-source-protection"
	const pvcFinalizer = "kubernetes.io/pvc-protection"
	deletionTime := metav1.NewTime(time.Date(2026, time.October, 12, 0, 0, 0, 0, time.UTC))
	claim := func(deleting bool, finalizers ...string) *corev1.PersistentVolumeClaim {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ordinary", Finalizers: finalizers},
			Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: "pv1"},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
		}
		if deleting {
			pvc.DeletionTimestamp = &deletionTime
		}
		return pvc
	}

	for _, tc := range []struct {
		name      string
		old, next *corev1.PersistentVolumeClaim
		allow     bool
	}{
		{"only snapshot finalizer removed in an ordinary namespace", claim(true, pvcFinalizer, snapshotFinalizer), claim(true, pvcFinalizer), true},
		{"live PVC", claim(false, pvcFinalizer, snapshotFinalizer), claim(false, pvcFinalizer), false},
		{"snapshot finalizer absent", claim(true, pvcFinalizer), claim(true, pvcFinalizer), false},
		{"other finalizer removed", claim(true, pvcFinalizer, snapshotFinalizer), claim(true), false},
		{"another finalizer added", claim(true, snapshotFinalizer), claim(true, "example.com/extra"), false},
		{"spec changed", claim(true, snapshotFinalizer), func() *corev1.PersistentVolumeClaim {
			next := claim(true)
			next.Spec.VolumeName = "pv2"
			return next
		}(), false},
		{"annotations changed", claim(true, snapshotFinalizer), func() *corev1.PersistentVolumeClaim {
			next := claim(true)
			next.Annotations = map[string]string{"example.com/key": "value"}
			return next
		}(), false},
		{"status changed", claim(true, snapshotFinalizer), func() *corev1.PersistentVolumeClaim {
			next := claim(true)
			next.Status.Phase = corev1.ClaimLost
			return next
		}(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tc.old)
			if err != nil {
				t.Fatal(err)
			}
			next, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tc.next)
			if err != nil {
				t.Fatal(err)
			}
			result, _, err := program.Eval(map[string]any{
				"oldObject": old, "object": next,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := result.Value().(bool); !ok || got != tc.allow {
				t.Fatalf("allowed = %v, want %v", result.Value(), tc.allow)
			}
		})
	}
}
