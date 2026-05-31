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

package controller

import (
	"context"
	"testing"
)

func TestValidate(t *testing.T) {
	validOptions := func() *RawControllerOptions {
		return &RawControllerOptions{
			CloudEnvironment:     "AzurePublicCloud",
			Region:               "westus3",
			CosmosURL:            "https://cosmos.example.com",
			CosmosName:           "fleet-db",
			ClustersServiceURL:   "https://cs.example.com",
			KubeNamespace:        "fleet-system",
			HealthzListenAddress: ":8080",
			MetricsListenAddress: ":8081",
			LeaderElectionID:     "fleet-controller",
		}
	}

	tests := []struct {
		name    string
		modify  func(opts *RawControllerOptions)
		wantErr bool
	}{
		{
			name:   "all fields set",
			modify: func(opts *RawControllerOptions) {},
		},
		{
			name:    "missing cosmos-url",
			modify:  func(opts *RawControllerOptions) { opts.CosmosURL = "" },
			wantErr: true,
		},
		{
			name:    "missing cosmos-name",
			modify:  func(opts *RawControllerOptions) { opts.CosmosName = "" },
			wantErr: true,
		},
		{
			name:    "missing region",
			modify:  func(opts *RawControllerOptions) { opts.Region = "" },
			wantErr: true,
		},
		{
			name:    "missing clusters-service-url",
			modify:  func(opts *RawControllerOptions) { opts.ClustersServiceURL = "" },
			wantErr: true,
		},
		{
			name:    "missing kube-namespace",
			modify:  func(opts *RawControllerOptions) { opts.KubeNamespace = "" },
			wantErr: true,
		},
		{
			name:    "invalid cloud-environment",
			modify:  func(opts *RawControllerOptions) { opts.CloudEnvironment = "InvalidCloud" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := validOptions()
			tt.modify(opts)

			_, err := opts.Validate(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}
