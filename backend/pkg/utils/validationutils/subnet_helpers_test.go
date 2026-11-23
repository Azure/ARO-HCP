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

package validationutils

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestIsSubnetReadPermissionDenied(t *testing.T) {
	subnetID, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg" +
			"/providers/Microsoft.Network/virtualNetworks/vnet/subnets/w3")
	if err != nil {
		t.Fatalf("failed to parse subnet ID: %v", err)
	}

	authzErr := &azcore.ResponseError{ErrorCode: "AuthorizationFailed", StatusCode: http.StatusForbidden}
	throttleErr := &azcore.ResponseError{ErrorCode: "TooManyRequests", StatusCode: http.StatusTooManyRequests}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"authorization failed is a denial", authzErr, true},
		{
			"wrapped authorization failed is a denial",
			fmt.Errorf("failed to get subnet %q: %w", subnetID.String(), authzErr),
			true,
		},
		{"throttling is not a denial (handled as before)", throttleErr, false},
		{"generic error is not a denial (handled as before)", errors.New("boom"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSubnetReadPermissionDenied(tc.err); got != tc.want {
				t.Fatalf("isSubnetReadPermissionDenied = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSubnetReadPermissionDeniedResult(t *testing.T) {
	subnetID, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg" +
			"/providers/Microsoft.Network/virtualNetworks/vnet/subnets/w3")
	if err != nil {
		t.Fatalf("failed to parse subnet ID: %v", err)
	}
	smiID, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg" +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi")
	if err != nil {
		t.Fatalf("failed to parse SMI ID: %v", err)
	}

	got := subnetReadPermissionDeniedResult(subnetID, smiID)

	if got.Outcome.Type != OutcomeTypeFailed {
		t.Fatalf("outcome = %v, want %v", got.Outcome.Type, OutcomeTypeFailed)
	}
	if got.Reason() != ReasonSubnetReadPermissionDenied {
		t.Fatalf("reason = %q, want %q", got.Reason(), ReasonSubnetReadPermissionDenied)
	}
	// A terminal denial must not carry an Unknown payload; the Unknown path is what
	// returns a controller error and drives the "Error syncing" storm.
	if got.Outcome.Unknown != nil {
		t.Fatalf("Failed result unexpectedly carries an Unknown payload")
	}
}

func TestResourceIDString(t *testing.T) {
	if got := resourceIDString(nil); got != "<unknown>" {
		t.Fatalf("resourceIDString(nil) = %q, want %q", got, "<unknown>")
	}
	id, err := azcorearm.ParseResourceID(
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg" +
			"/providers/Microsoft.Network/virtualNetworks/vnet/subnets/w3")
	if err != nil {
		t.Fatalf("failed to parse resource ID: %v", err)
	}
	if got := resourceIDString(id); got != id.String() {
		t.Fatalf("resourceIDString(id) = %q, want %q", got, id.String())
	}
}
