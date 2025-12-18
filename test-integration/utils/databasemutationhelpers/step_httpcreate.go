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
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

type httpCreateStep struct {
	stepID StepID
	key    FrontendResourceKey

	resources [][]byte
}

func newHTTPCreateStep(stepID StepID, stepDir fs.FS) (*httpCreateStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key FrontendResourceKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	resources, err := readRawBytesInDir(stepDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource in dir: %w", err)
	}

	return &httpCreateStep{
		stepID:    stepID,
		key:       key,
		resources: resources,
	}, nil
}

var _ IntegrationTestStep = &httpCreateStep{}

func (l *httpCreateStep) StepID() StepID {
	return l.stepID
}

func (l *httpCreateStep) RunTest(ctx context.Context, t *testing.T, stepInput StepInput) {
	subscriptionID := api.Must(azcorearm.ParseResourceID(l.key.ResourceID)).SubscriptionID
	accessor := newFrontendHTTPTestAccessor(stepInput.FrontendURL, stepInput.FrontendClient(subscriptionID))

	for _, resource := range l.resources {
		err := accessor.CreateOrUpdate(ctx, l.key.ResourceID, resource)
		require.NoError(t, err)
	}
}
