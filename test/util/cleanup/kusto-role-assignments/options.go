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

package kustoroleassignments

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto/v2"

	"github.com/Azure/ARO-HCP/test/util/framework"
)

type RawOptions struct {
}

type ValidatedOptions struct {
}

type Options struct {
	Client *armkusto.DatabasePrincipalAssignmentsClient
}

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	return &ValidatedOptions{}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {

	tc := framework.NewTestContext()

	subscriptionID, err := tc.SubscriptionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription ID: %w", err)
	}

	credential, err := tc.AzureCredential()
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure credential: %w", err)
	}

	databasePrincipalAssignmentsClient, err := armkusto.NewDatabasePrincipalAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create database principal assignments client: %w", err)
	}

	return &Options{
		Client: databasePrincipalAssignmentsClient,
	}, nil
}
