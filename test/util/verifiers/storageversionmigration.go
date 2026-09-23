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

package verifiers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// HyperShift uses the out-of-tree migration.k8s.io CRD (not the in-tree storagemigration.k8s.io),
// which has no typed client in client-go, so we use the dynamic client.
var svmGVR = schema.GroupVersionResource{
	Group:    "migration.k8s.io",
	Version:  "v1alpha1",
	Resource: "storageversionmigrations",
}

// VerifyStorageVersionMigrationSucceeded returns a verifier that checks all
// encryption-migration-* StorageVersionMigration resources are in Succeeded state.
// HCCO re-encryption controller creates StorageVersionMigration resources with this prefix
// https://github.com/muraee/enhancements/blob/hypershift-etcd-reencryption/enhancements/hypershift/etcd-data-reencryption-on-key-rotation.md#component-2-re-encryption-orchestration-hcco
func VerifyStorageVersionMigrationSucceeded() HostedClusterVerifier {
	return verifyStorageVersionMigrationSucceeded{}
}

type verifyStorageVersionMigrationSucceeded struct{}

func (v verifyStorageVersionMigrationSucceeded) Name() string {
	return "VerifyStorageVersionMigrationSucceeded"
}

func (v verifyStorageVersionMigrationSucceeded) Verify(ctx context.Context, restConfig *rest.Config) error {
	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	svmList, err := dynClient.Resource(svmGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StorageVersionMigration resources: %w", err)
	}

	var encryptionMigrations []unstructured.Unstructured
	for i := range svmList.Items {
		if strings.HasPrefix(svmList.Items[i].GetName(), "encryption-migration-") {
			encryptionMigrations = append(encryptionMigrations, svmList.Items[i])
		}
	}

	if len(encryptionMigrations) == 0 {
		return fmt.Errorf("no encryption-migration-* StorageVersionMigration resources found")
	}

	var notSucceeded []string
	for i := range encryptionMigrations {
		if !storageVersionMigrationSucceeded(&encryptionMigrations[i]) {
			notSucceeded = append(notSucceeded, encryptionMigrations[i].GetName())
		}
	}

	if len(notSucceeded) > 0 {
		sort.Strings(notSucceeded)
		summaries := summarizeMigrations(encryptionMigrations)
		summariesJSON, err := json.Marshal(summaries)
		if err != nil {
			return fmt.Errorf("%d encryption StorageVersionMigration resources not Succeeded: %s", len(notSucceeded), strings.Join(notSucceeded, ", "))
		}
		return fmt.Errorf("%d encryption StorageVersionMigration resources not Succeeded: %s; migrations=%s",
			len(notSucceeded), strings.Join(notSucceeded, ", "), string(summariesJSON))
	}

	return nil
}

type migrationSummary struct {
	Name       string             `json:"name"`
	Resource   string             `json:"resource,omitempty"`
	Conditions []conditionSummary `json:"conditions,omitempty"`
}

type conditionSummary struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func summarizeMigrations(migrations []unstructured.Unstructured) []migrationSummary {
	summaries := make([]migrationSummary, len(migrations))
	for i := range migrations {
		obj := &migrations[i]
		s := migrationSummary{Name: obj.GetName()}

		resource, _, _ := unstructured.NestedString(obj.Object, "spec", "resource", "resource")
		if resource != "" {
			group, _, _ := unstructured.NestedString(obj.Object, "spec", "resource", "group")
			if group != "" {
				s.Resource = group + "/" + resource
			} else {
				s.Resource = resource
			}
		}

		conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err == nil && found {
			for _, c := range conditions {
				cond, ok := c.(map[string]interface{})
				if !ok {
					continue
				}
				t, _ := cond["type"].(string)
				status, _ := cond["status"].(string)
				s.Conditions = append(s.Conditions, conditionSummary{Type: t, Status: status})
			}
		}

		summaries[i] = s
	}
	return summaries
}

func storageVersionMigrationSucceeded(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == "Succeeded" && cond["status"] == "True" {
			return true
		}
	}
	return false
}
