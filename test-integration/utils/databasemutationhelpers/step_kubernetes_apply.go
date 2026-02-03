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
)

type kubernetesApplyStep struct {
	stepID StepID

	contents [][]byte
}

func NewKubernetesApplyStep(stepID StepID, stepDir fs.FS) (*kubernetesApplyStep, error) {
	contents, err := readRawBytesInDir(stepDir)
	if err != nil {
		return nil, err
	}
	return &kubernetesApplyStep{
		stepID:   stepID,
		contents: contents,
	}, nil
}

var _ IntegrationTestStep = &kubernetesApplyStep{}

func (k *kubernetesApplyStep) StepID() StepID {
	return k.stepID
}

func (k *kubernetesApplyStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	if stepInput.KubeFakeClientSets == nil {
		t.Fatal("KubeFakeClientSets must not be nil in kubernetesApplyStep, probably using from the wrong kind of test")
	}
	for _, content := range k.contents {
		err := stepInput.KubeFakeClientSets.Apply(ctx, content)
		if err != nil {
			t.Fatalf("failed to apply resource: %v", err)
		}
	}
}
