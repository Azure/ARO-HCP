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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
)

// The definitions in this file are meant for unit tests.

func newNoopAuditClient(t *testing.T) *audit.AuditClient {
	c, err := audit.NewOtelAuditClient(audit.CreateConn(false))
	require.NoError(t, err)
	return c
}

func NewTestFrontend(t *testing.T) *Frontend {
	ctrl := gomock.NewController(t)
	mockDBClient := database.NewMockDBClient(ctrl)
	reg := prometheus.NewRegistry()

	f := NewFrontend(
		api.NewTestLogger(),
		nil,
		nil,
		reg,
		mockDBClient,
		nil,
		newNoopAuditClient(t),
		api.TestLocation,
	)
	return f
}
