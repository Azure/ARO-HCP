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

package customize

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSanityCheckOperatorDeployment(t *testing.T) {
	deployment, err := convertToUnstructured(buildMulticlusterEngineDeployment())
	assert.Nil(t, err)

	err = SanityCheck([]unstructured.Unstructured{deployment})
	assert.Nil(t, err)
}

func TestSanityCheckNoOperatorDeployment(t *testing.T) {
	deployment, err := convertToUnstructured(buildDeployment("some-deployment", "some-image", nil))
	assert.Nil(t, err)

	err = SanityCheck([]unstructured.Unstructured{deployment})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no operator deployment found in the bundle")
}

func TestSanityCheckOperatorDeploymentNoOperandEnvVars(t *testing.T) {
	deployment := buildMulticlusterEngineDeployment()
	deployment.Spec.Template.Spec.Containers[0].Env = nil
	obj, err := convertToUnstructured(deployment)
	assert.Nil(t, err)

	err = SanityCheck([]unstructured.Unstructured{obj})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no operand image env vars found in the operator deployment")
}
