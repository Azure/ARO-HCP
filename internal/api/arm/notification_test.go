// Copyright 2025 Microsoft Corporation
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

package arm

import (
	"testing"
)

func TestValidateNotificationURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{
			name:    "empty string is valid",
			uri:     "",
			wantErr: false,
		},
		{
			name:    "valid ARM public endpoint",
			uri:     "https://management.azure.com/providers/Microsoft.Resources/operationResults/abc123",
			wantErr: false,
		},
		{
			name:    "valid ARM US Gov endpoint",
			uri:     "https://management.usgovcloudapi.net/providers/Microsoft.Resources/operationResults/abc123",
			wantErr: false,
		},
		{
			name:    "case insensitive URI",
			uri:     "HTTPS://Management.azure.com/providers/Microsoft.Resources/operationResults/abc123",
			wantErr: false,
		},
		{
			name:    "HTTP scheme rejected",
			uri:     "http://management.azure.com/providers/Microsoft.Resources/operationResults/abc123",
			wantErr: true,
		},
		{
			name:    "unknown host rejected",
			uri:     "https://evil.example.com/callback",
			wantErr: true,
		},
		{
			name:    "subdomain of allowed host rejected",
			uri:     "https://foo.management.azure.com/callback",
			wantErr: true,
		},
		{
			name:    "no scheme rejected",
			uri:     "management.azure.com/callback",
			wantErr: true,
		},
		{
			name:    "javascript scheme rejected",
			uri:     "javascript:alert(1)",
			wantErr: true,
		},
		{
			name:    "file scheme rejected",
			uri:     "file:///etc/passwd",
			wantErr: true,
		},
		{
			name:    "localhost rejected",
			uri:     "https://localhost/callback",
			wantErr: true,
		},
		{
			name:    "IP address rejected",
			uri:     "https://169.254.169.254/latest/meta-data",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNotificationURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNotificationURI(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
		})
	}
}
