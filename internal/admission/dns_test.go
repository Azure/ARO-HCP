// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package admission

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func TestMutateClusterDNS(t *testing.T) {
	for _, tt := range []struct {
		name         string
		op           operation.Type
		prefix, want string
	}{
		{name: "default new cluster", op: operation.Create, want: "cluster"},
		{name: "custom new cluster", op: operation.Create, prefix: "payments", want: "payments"},
		{name: "legacy update awaits backfill", op: operation.Update},
		{name: "preserve update", op: operation.Update, prefix: "legacy", want: "legacy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			profile := coreapi.CustomerDNSProfile{BaseDomainPrefix: tt.prefix}
			require.Empty(t, mutateClusterDNS(t.Context(), nil, operation.Operation{Type: tt.op}, field.NewPath("dns"), &profile))
			require.Equal(t, tt.want, profile.BaseDomainPrefix)
		})
	}
}
