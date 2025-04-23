package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the apache License 2.0.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/stretchr/testify/assert"
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
			name:      "parse v1 cluster",
			path:      "/api/aro_hcp/v1alpha1/clusters/abc",
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
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			transport := &FakeTransport{}
			_, ok := internalID.GetClusterClient(transport)
			assert.NotEqual(t, tt.expectErr, ok)
			_, ok = internalID.GetAroHCPClusterClient(transport)
			assert.NotEqual(t, tt.expectErr, ok)

			if tt.expectErr {
				// test ends here if error is expected
				return
			}

			id := internalID.ID()
			assert.Equal(t, tt.id, id)

			kind := internalID.Kind()
			assert.Equal(t, tt.kind, kind)

			str := internalID.String()
			assert.Equal(t, tt.path, str)

			if kind == cmv1.NodePoolKind {
				_, ok := internalID.GetNodePoolClient(transport)
				assert.True(t, ok, "failed to get node pool client")
			}

			bytes, err := json.Marshal(internalID)
			if assert.NoError(t, err) {
				fmt.Printf("Bytes: %s\n", bytes)
				err = json.Unmarshal(bytes, &internalID)
				assert.NoError(t, err)
			}
		})
	}
}
