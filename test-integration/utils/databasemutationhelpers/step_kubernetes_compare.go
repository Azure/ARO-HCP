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

package databasemutationhelpers

import (
	"context"
	"io/fs"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type kubernetesCompareStep struct {
	stepID StepID

	expectedContent []*unstructured.Unstructured
}

func NewKubernetesCompareStep(stepID StepID, stepDir fs.FS) (*kubernetesCompareStep, error) {
	expectedContent, err := readResourcesInDir[unstructured.Unstructured](stepDir)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return &kubernetesCompareStep{
		stepID:          stepID,
		expectedContent: expectedContent,
	}, nil
}

var _ IntegrationTestStep = &kubernetesCompareStep{}

func (k *kubernetesCompareStep) StepID() StepID {
	return k.stepID
}

func (k *kubernetesCompareStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	if stepInput.KubeFakeClientSets == nil {
		t.Fatal("KubeFakeClientSets must not be nil in kubernetesCompareStep, probably using from the wrong kind of test")
	}

	for _, expected := range k.expectedContent {
		tracked, err := stepInput.KubeFakeClientSets.GetTrackedObject(ctx, expected)
		if err != nil {
			t.Fatalf("failed to find tracked object: %v", err)
		}
		diff, equals := ResourceInstanceEquals(t, expected, tracked)
		if !equals {
			t.Log(diff)
			t.Fatalf("expected object is not tracked")
		}
	}
}
