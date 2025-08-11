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

package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureDefaults(t *testing.T) {
	expected := `{"CallerIpAddress":"192.168.1.1","CallerIdentities":{"10":[{"Identity":"Unknown","Description":"Unknown"}]},"OperationCategories":[10],"TargetResources":{"Unknown":[{"Name":"Unknown","Cluster":"","DataCenter":"","Region":"Unknown"}]},"CallerAccessLevels":["Unknown"],"OperationAccessLevel":"Unknown","OperationName":"Unknown","OperationResultDescription":"","CallerAgent":"Unknown","OperationCategoryDescription":"","OperationType":0,"OperationResult":0}`

	record := &msgs.Record{}
	ensureDefaults(record)

	current, err := json.Marshal(record)
	require.NoError(t, err)

	assert.Equal(t, expected, string(current))
}

func TestConnect(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	f := strings.Split(testServer.URL, "/")
	_, err := initializeTcpOtelAuditClient(-1, f[2])
	require.NoError(t, err)

	_, err = initializeTcpOtelAuditClient(-1, "127.0.0.1:12345")
	require.Error(t, err, "error creating audit client dial tcp 127.0.0.1:12345: connect: connection refused")
}
