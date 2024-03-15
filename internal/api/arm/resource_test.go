package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding"
	"encoding/json"
	"fmt"
	"testing"
)

func TestCreatedByType(t *testing.T) {
	// Ensure CreatedByType implements these interfaces
	var i CreatedByType
	_ = fmt.Stringer(i)
	_ = encoding.TextMarshaler(i)
	_ = encoding.TextUnmarshaler(&i)

	for _, tt := range []struct {
		name    string
		val     int
		str     string
		wantErr bool
	}{
		{
			name: "CreatedByTypeApplication",
			val:  int(CreatedByTypeApplication),
			str:  fmt.Sprintf("%q", CreatedByTypeApplication),
		},
		{
			name: "CreatedByTypeKey",
			val:  int(CreatedByTypeKey),
			str:  fmt.Sprintf("%q", CreatedByTypeKey),
		},
		{
			name: "CreatedByTypeManagedIdentity",
			val:  int(CreatedByTypeManagedIdentity),
			str:  fmt.Sprintf("%q", CreatedByTypeManagedIdentity),
		},
		{
			name: "CreatedByTypeUser",
			val:  int(CreatedByTypeUser),
			str:  fmt.Sprintf("%q", CreatedByTypeUser),
		},
		{
			name:    "Invalid CreatedByType",
			val:     -1,
			str:     "\"invalid\"",
			wantErr: true,
		},
	} {
		t.Logf("Marshaling %d", tt.val)
		data, err := json.Marshal(CreatedByType(tt.val))
		if err != nil {
			if tt.wantErr {
				t.Logf("Marshal: Got expected error: %s", err)
			} else {
				t.Fatalf("Marshal: Unexpected error: %s", err)
			}
		} else if tt.wantErr {
			t.Fatal("Marshal: Expected error but got none")
		} else if string(data) != tt.str {
			t.Fatalf("Marshal: Expected %s, got %s", tt.str, string(data))
		}

		var val CreatedByType
		t.Logf("Unmarshaling %s", tt.str)
		err = json.Unmarshal([]byte(tt.str), &val)
		if err != nil {
			if tt.wantErr {
				t.Logf("Unmarshal: Got expected error: %s", err)
			} else {
				t.Fatalf("Unmarshal: Unexpected error: %s", err)
			}
		} else if tt.wantErr {
			t.Fatal("Unmarshal: Expected error but got none")
		} else if int(val) != tt.val {
			t.Fatalf("Unmarshal: Expected %d, got %d", tt.val, val)
		}
	}
}

func TestProvisioningState(t *testing.T) {
	// Ensure ProvisioningState implements these interfaces
	var i ProvisioningState
	_ = fmt.Stringer(i)
	_ = encoding.TextMarshaler(i)
	_ = encoding.TextUnmarshaler(&i)

	for _, tt := range []struct {
		name    string
		val     int
		str     string
		wantErr bool
	}{
		{
			name: "ProvisioningStateSucceeded",
			val:  int(ProvisioningStateSucceeded),
			str:  fmt.Sprintf("%q", ProvisioningStateSucceeded),
		},
		{
			name: "ProvisioningStateFailed",
			val:  int(ProvisioningStateFailed),
			str:  fmt.Sprintf("%q", ProvisioningStateFailed),
		},
		{
			name: "ProvisioningStateCanceled",
			val:  int(ProvisioningStateCanceled),
			str:  fmt.Sprintf("%q", ProvisioningStateCanceled),
		},
		{
			name: "ProvisioningStateAccepted",
			val:  int(ProvisioningStateAccepted),
			str:  fmt.Sprintf("%q", ProvisioningStateAccepted),
		},
		{
			name: "ProvisioningStateDeleting",
			val:  int(ProvisioningStateDeleting),
			str:  fmt.Sprintf("%q", ProvisioningStateDeleting),
		},
		{
			name: "ProvisioningStateProvisioning",
			val:  int(ProvisioningStateProvisioning),
			str:  fmt.Sprintf("%q", ProvisioningStateProvisioning),
		},
		{
			name: "ProvisioningStateUpdating",
			val:  int(ProvisioningStateUpdating),
			str:  fmt.Sprintf("%q", ProvisioningStateUpdating),
		},
		{
			name:    "Invalid ProvisioningState",
			val:     -1,
			str:     "\"invalid\"",
			wantErr: true,
		},
	} {
		t.Logf("Marshaling %d", tt.val)
		data, err := json.Marshal(ProvisioningState(tt.val))
		if err != nil {
			if tt.wantErr {
				t.Logf("Marshal: Got expected error: %s", err)
			} else {
				t.Fatalf("Marshal: Unexpected error: %s", err)
			}
		} else if tt.wantErr {
			t.Fatal("Marshal: Expected error but got none")
		} else if string(data) != tt.str {
			t.Fatalf("Marshal: Expected %s, got %s", tt.str, string(data))
		}

		var val ProvisioningState
		t.Logf("Unmarshaling %s", tt.str)
		err = json.Unmarshal([]byte(tt.str), &val)
		if err != nil {
			if tt.wantErr {
				t.Logf("Unmarshal: Got expected error: %s", err)
			} else {
				t.Fatalf("Unmarshal: Unexpected error: %s", err)
			}
		} else if tt.wantErr {
			t.Fatal("Unmarshal: Expected error but got none")
		} else if int(val) != tt.val {
			t.Fatalf("Unmarshal: Expected %d, got %d", tt.val, val)
		}
	}
}
