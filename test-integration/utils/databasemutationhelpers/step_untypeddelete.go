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
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
)

type UntypedDeleteKey struct {
	UntypedCRUDKey `json:",inline"`

	DeleteResourceID string `json:"deleteResourceId"`
}

type untypedDeleteStep struct {
	stepID      StepID
	key         UntypedDeleteKey
	specializer ResourceCRUDTestSpecializer[database.TypedDocument]

	cosmosContainer *azcosmos.ContainerClient
	expectedError   string
}

func newUntypedDeleteStep(stepID StepID, cosmosContainer *azcosmos.ContainerClient, stepDir fs.FS) (*untypedDeleteStep, error) {
	keyBytes, err := fs.ReadFile(stepDir, "00-key.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read key.json: %w", err)
	}
	var key UntypedDeleteKey
	if err := json.Unmarshal(keyBytes, &key); err != nil {
		return nil, fmt.Errorf("failed to unmarshal key.json: %w", err)
	}

	expectedErrorBytes, err := fs.ReadFile(stepDir, "expected-error.txt")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to read expected-error.txt: %w", err)
	}
	expectedError := strings.TrimSpace(string(expectedErrorBytes))

	return &untypedDeleteStep{
		stepID:          stepID,
		key:             key,
		specializer:     UntypedCRUDSpecializer{},
		cosmosContainer: cosmosContainer,
		expectedError:   expectedError,
	}, nil
}

var _ IntegrationTestStep = &untypedDeleteStep{}

func (l *untypedDeleteStep) StepID() StepID {
	return l.stepID
}

func (l *untypedDeleteStep) RunTest(ctx context.Context, t *testing.T) {
	parentResourceID, err := azcorearm.ParseResourceID(l.key.ParentResourceID)
	require.NoError(t, err)

	untypedCRUD := database.NewUntypedCRUD(l.cosmosContainer, *parentResourceID)
	for _, childKey := range l.key.Descendents {
		childResourceType, err := azcorearm.ParseResourceType(childKey.ResourceType)
		require.NoError(t, err)
		untypedCRUD, err = untypedCRUD.Child(childResourceType, childKey.ResourceName)
		require.NoError(t, err)
	}
	err = untypedCRUD.Delete(ctx, api.Must(azcorearm.ParseResourceID(l.key.DeleteResourceID)))
	switch {
	case len(l.expectedError) > 0:
		require.ErrorContains(t, err, l.expectedError)
		return
	default:
		require.NoError(t, err)
	}
}
