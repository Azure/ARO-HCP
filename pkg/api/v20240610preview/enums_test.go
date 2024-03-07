package v20240610preview

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding"
	"encoding/json"
	"fmt"
	"testing"
)

func TestOutboundType(t *testing.T) {
	// Ensure OutboundType implements these interfaces
	var i OutboundType
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
			name: "OutboundTypeLoadBalancer",
			val:  int(OutboundTypeLoadBalancer),
			str:  fmt.Sprintf("%q", OutboundTypeLoadBalancer),
		},
		{
			name:    "Invalid OutboundType",
			val:     -1,
			str:     "\"invalid\"",
			wantErr: true,
		},
	} {
		t.Logf("Marshaling %d", tt.val)
		data, err := json.Marshal(OutboundType(tt.val))
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

		var val OutboundType
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

func TestVisibility(t *testing.T) {
	// Ensure Visibility implements these interfaces
	var i Visibility
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
			name: "VisibilityPublic",
			val:  int(VisibilityPublic),
			str:  fmt.Sprintf("%q", VisibilityPublic),
		},
		{
			name: "VisibilityPrivate",
			val:  int(VisibilityPrivate),
			str:  fmt.Sprintf("%q", VisibilityPrivate),
		},
		{
			name:    "Invalid Visibility",
			val:     -1,
			str:     "\"invalid\"",
			wantErr: true,
		},
	} {
		t.Logf("Marshaling %d", tt.val)
		data, err := json.Marshal(Visibility(tt.val))
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

		var val Visibility
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
