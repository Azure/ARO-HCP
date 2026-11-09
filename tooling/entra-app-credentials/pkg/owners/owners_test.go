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

package owners

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	alice   = "11111111-1111-1111-1111-111111111111"
	bob     = "22222222-2222-2222-2222-222222222222"
	carol   = "33333333-3333-3333-3333-333333333333"
	deleted = "44444444-4444-4444-4444-444444444444"
	gone    = "55555555-5555-5555-5555-555555555555"
	group   = "66666666-6666-6666-6666-666666666666"
)

type fakeDirectory struct {
	members   []Member
	objects   map[string]*Object
	listErr   error
	lookupErr error
}

func (f *fakeDirectory) GroupMembers(context.Context, string) ([]Member, error) {
	return f.members, f.listErr
}

func (f *fakeDirectory) Lookup(_ context.Context, id string) (*Object, error) {
	return f.objects[id], f.lookupErr
}

func TestCheckPassesWhenConfigMatchesGroup(t *testing.T) {
	directory := &fakeDirectory{members: []Member{{ID: alice}, {ID: "22222222-2222-2222-2222-222222222222"}}}

	require.NoError(t, Check(t.Context(), directory, group, []string{bob, alice}))
}

func TestCheckComparesIDsCaseInsensitively(t *testing.T) {
	directory := &fakeDirectory{members: []Member{{ID: "AAAAAAAA-1111-1111-1111-111111111111"}}}

	require.NoError(t, Check(t.Context(), directory, group, []string{"aaaaaaaa-1111-1111-1111-111111111111"}))
}

func TestCheckReportsDriftInBothDirections(t *testing.T) {
	directory := &fakeDirectory{
		members: []Member{{ID: alice, DisplayName: "Alice"}, {ID: carol, DisplayName: "Carol"}, {ID: bob, DisplayName: "Bob"}},
		objects: map[string]*Object{
			deleted: {DisplayName: "Dana", Deleted: true},
			alice:   {DisplayName: "Alice"},
			// bob exists but is configured-out; dave exists and is not in the group.
			"77777777-7777-7777-7777-777777777777": {DisplayName: "Dave"},
		},
	}

	err := Check(t.Context(), directory, group, []string{alice, deleted, gone, "77777777-7777-7777-7777-777777777777"})

	var drift *Drift
	require.ErrorAs(t, err, &drift)
	assert.Equal(t, []Stale{
		{ID: deleted, Reason: `deleted account "Dana" (in the Entra recycle bin)`},
		{ID: gone, Reason: "does not exist in the tenant"},
		{ID: "77777777-7777-7777-7777-777777777777", Reason: `"Dave" is not a member of the group`},
	}, drift.Stale)
	assert.Equal(t, []Member{{ID: bob, DisplayName: "Bob"}, {ID: carol, DisplayName: "Carol"}}, drift.Missing)

	message := err.Error()
	assert.Contains(t, message, group)
	assert.Contains(t, message, "Remove these IDs from entraAppOwnerIds")
	assert.Contains(t, message, "Add these group members to entraAppOwnerIds")
	assert.Contains(t, message, deleted+"  deleted account")
	assert.Contains(t, message, bob+"  Bob")
}

func TestCheckOmitsEmptySectionsFromMessage(t *testing.T) {
	directory := &fakeDirectory{members: []Member{{ID: alice}}, objects: map[string]*Object{}}

	err := Check(t.Context(), directory, group, []string{alice, gone})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Remove these IDs")
	assert.NotContains(t, err.Error(), "Add these group members")
}

func TestCheckPropagatesDirectoryErrors(t *testing.T) {
	boom := errors.New("boom")

	err := Check(t.Context(), &fakeDirectory{listErr: boom}, group, []string{alice})
	require.ErrorIs(t, err, boom)

	err = Check(t.Context(), &fakeDirectory{members: []Member{{ID: bob}}, lookupErr: boom}, group, []string{alice})
	require.ErrorIs(t, err, boom)
	var drift *Drift
	assert.False(t, errors.As(err, &drift))
}

func TestParseIDs(t *testing.T) {
	ids, err := ParseIDs(" AAAAAAAA-1111-1111-1111-111111111111, " + bob + ",")
	require.NoError(t, err)
	assert.Equal(t, []string{"aaaaaaaa-1111-1111-1111-111111111111", bob}, ids)

	for name, input := range map[string]string{
		"empty":                     "",
		"blank":                     " , ",
		"malformed":                 alice + ",not-a-guid",
		"duplicate":                 alice + "," + alice,
		"duplicate, different case": "AAAAAAAA-1111-1111-1111-111111111111,aaaaaaaa-1111-1111-1111-111111111111",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseIDs(input)
			assert.Error(t, err)
		})
	}
}
