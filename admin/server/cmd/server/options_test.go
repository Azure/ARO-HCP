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

package server

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestValidateAuditServiceTreeID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		enabled bool
		wantErr bool
	}{
		{name: "empty", wantErr: true},
		{name: "malformed", id: "not-a-uuid", wantErr: true},
		{name: "disabled zero", id: uuid.Nil.String()},
		{name: "enabled zero", id: uuid.Nil.String(), enabled: true, wantErr: true},
		{name: "enabled identity", id: "11111111-1111-4111-8111-111111111111", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Location = "westus3"
			opts.ClustersServiceURL = "https://localhost"
			opts.CosmosURL = "https://localhost"
			opts.CosmosName = "test"
			opts.SessiongateNamespace = "sessiongate"
			opts.MinSessionTTL = 10 * time.Minute
			opts.MaxSessionTTL = 24 * time.Hour
			opts.AuditServiceTreeID = tc.id
			opts.AuditConnectSocket = tc.enabled
			_, err := opts.Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
