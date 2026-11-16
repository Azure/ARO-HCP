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

package checkowners

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/owners"
)

func validOptions() *RawOptions {
	return &RawOptions{
		GroupID:  "66666666-6666-6666-6666-666666666666",
		OwnerIDs: "11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222",
		Timeout:  time.Minute,
	}
}

func TestValidateAcceptsOptions(t *testing.T) {
	validated, err := validOptions().Validate()

	require.NoError(t, err)
	assert.Len(t, validated.ownerIDs, 2)
}

func TestValidateRejectsInvalidInput(t *testing.T) {
	for name, mutate := range map[string]func(*RawOptions){
		"missing group":    func(o *RawOptions) { o.GroupID = "" },
		"group not a GUID": func(o *RawOptions) { o.GroupID = "aro-hcp-owners" },
		"no owner IDs":     func(o *RawOptions) { o.OwnerIDs = "" },
		"bad owner ID":     func(o *RawOptions) { o.OwnerIDs = "nope" },
		"zero timeout":     func(o *RawOptions) { o.Timeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			opts := validOptions()
			mutate(opts)
			_, err := opts.Validate()
			assert.Error(t, err)
		})
	}
}

type fakeDirectory struct{ members []owners.Member }

func (f fakeDirectory) GroupMembers(context.Context, string) ([]owners.Member, error) {
	return f.members, nil
}

func (fakeDirectory) Lookup(context.Context, string) (*owners.Object, error) { return nil, nil }

func TestRunWritesDriftReportAndFails(t *testing.T) {
	var report bytes.Buffer
	options := &Options{
		ValidatedOptions: &ValidatedOptions{
			groupID:  "66666666-6666-6666-6666-666666666666",
			ownerIDs: []string{"11111111-1111-1111-1111-111111111111"},
			timeout:  time.Minute,
		},
		directory: fakeDirectory{},
		report:    &report,
	}

	err := options.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, report.String(), "11111111-1111-1111-1111-111111111111  does not exist in the tenant")
}
