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

package frontend

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

// The definitions in this file are meant for unit tests.

type noopAuditClient struct{}

func (noopAuditClient) Send(context.Context, msgs.Msg) error {
	return nil
}

func newNoopAuditClient(t *testing.T) audit.Client {
	return noopAuditClient{}
}

func NewTestFrontend(t *testing.T) *Frontend {
	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	reg := prometheus.NewRegistry()

	f := NewFrontend(
		testr.New(t),
		nil,
		nil,
		reg,
		reg,
		mockResourcesDBClient,
		nil,
		newNoopAuditClient(t),
		coreapitesting.TestLocation,
		true,
	)
	return f
}
