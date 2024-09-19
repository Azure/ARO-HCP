package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the apache License 2.0.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type FakeTransport struct{}

func (t *FakeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func TestInternalID(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		id        string
		kind      string
		expectErr bool
	}{
		{
			name:      "parse invalid internal ID",
			path:      "/invalid/internal/id",
			kind:      "",
			expectErr: true,
		},
		{
			name:      "parse v1 cluster",
			path:      "/api/clusters_mgmt/v1/clusters/abc",
			id:        "abc",
			kind:      cmv1.ClusterKind,
			expectErr: false,
		},
		{
			name:      "parse v1 node pool",
			path:      "/api/clusters_mgmt/v1/clusters/abc/node_pools/def",
			id:        "def",
			kind:      cmv1.NodePoolKind,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			internalID, err := NewInternalID(tt.path)
			if err != nil {
				if !tt.expectErr {
					t.Error(err)
				}
				return
			}

			if tt.expectErr {
				t.Error("expected unmarshaling to fail")
				return
			}

			id := internalID.ID()
			if id != tt.id {
				t.Errorf("expected id '%s', got '%s'", tt.id, id)
			}

			kind := internalID.Kind()
			if kind != tt.kind {
				t.Errorf("expected kind '%s', got '%s'", tt.kind, kind)
			}

			str := internalID.String()
			if str != tt.path {
				t.Errorf("expected string '%s', got '%s'", tt.path, str)
			}

			transport := &FakeTransport{}
			if _, ok := internalID.GetClusterClient(transport); !ok {
				t.Errorf("failed to get cluster client")
			}
			if kind == cmv1.NodePoolKind {
				if _, ok := internalID.GetNodePoolClient(transport); !ok {
					t.Errorf("failed to get node pool client")
				}
			}

			bytes, err := json.Marshal(internalID)
			if err != nil {
				t.Error(err)
			}
			fmt.Printf("Bytes: %s\n", bytes)
			err = json.Unmarshal(bytes, &internalID)
			if err != nil {
				t.Error(err)
			}
		})
	}
}
