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
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	storagemigrationv1beta1 "k8s.io/api/storagemigration/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// VerifyStorageVersionMigrationSucceeded returns a verifier that polls until all
// StorageVersionMigration resources are in "Succeeded" state. This is important
// after rotating KMS encryption keys to ensure all Kubernetes objects have been
// re-encrypted with the new key.
func VerifyStorageVersionMigrationSucceeded(timeout time.Duration) HostedClusterVerifier {
	return verifyStorageVersionMigrationSucceeded{timeout: timeout}
}

type verifyStorageVersionMigrationSucceeded struct {
	timeout time.Duration
}

func (v verifyStorageVersionMigrationSucceeded) Name() string {
	return "VerifyStorageVersionMigrationSucceeded"
}

func (v verifyStorageVersionMigrationSucceeded) Verify(ctx context.Context, restConfig *rest.Config) error {
	return pollUntilReady(ctx, v.Name(), v.timeout, DefaultPollInterval, restConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		return v.checkOnce(ctx, restConfig)
	})
}

func (v verifyStorageVersionMigrationSucceeded) checkOnce(ctx context.Context, restConfig *rest.Config) error {
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	svmList, err := kubeClient.StoragemigrationV1beta1().StorageVersionMigrations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list StorageVersionMigration resources: %w", err)
	}

	if len(svmList.Items) == 0 {
		return fmt.Errorf("no StorageVersionMigration resources found - expected at least one after KMS key rotation")
	}

	var failedMigrations []string
	for i := range svmList.Items {
		if !storageVersionMigrationSucceeded(&svmList.Items[i]) {
			failedMigrations = append(failedMigrations, fmt.Sprintf("%s: not in Succeeded state", svmList.Items[i].GetName()))
		}
	}

	if len(failedMigrations) > 0 {
		sort.Strings(failedMigrations)
		return fmt.Errorf("StorageVersionMigration verification failed:\n%s", strings.Join(failedMigrations, "\n"))
	}

	return nil
}

// storageVersionMigrationSucceeded returns true if the StorageVersionMigration has Succeeded condition status True.
func storageVersionMigrationSucceeded(svm *storagemigrationv1beta1.StorageVersionMigration) bool {
	return slices.ContainsFunc(svm.Status.Conditions, func(c metav1.Condition) bool {
		return c.Type == "Succeeded" && c.Status == metav1.ConditionTrue
	})
}
