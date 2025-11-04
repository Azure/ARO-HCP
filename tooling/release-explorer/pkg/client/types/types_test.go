package types

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

func TestReleaseDeployment_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    *ReleaseDeployment
		wantErr bool
	}{
		{
			name:    "old format with RegionConfigs",
			fixture: "testdata/release_old_format.yaml",
			want: &ReleaseDeployment{
				Metadata: ReleaseMetadata{
					ReleaseId: ReleaseId{
						SourceRevision:   "43697e5fa59a",
						PipelineRevision: "000779a4",
					},
					Branch:           "main",
					Timestamp:        "2025-09-21T00:38:14Z",
					PullRequestID:    13525689,
					ServiceGroup:     "Microsoft.Azure.ARO.HCP.Global",
					ServiceGroupBase: "Microsoft.Azure.ARO.HCP",
				},
				Target: DeploymentTarget{
					Cloud:         "public",
					Environment:   "int",
					RegionConfigs: []string{"uksouth"},
				},
				Components: make(Components),
			},
			wantErr: false,
		},
		{
			name:    "old format with multiple RegionConfigs",
			fixture: "testdata/release_multiple_regions.yaml",
			want: &ReleaseDeployment{
				Metadata: ReleaseMetadata{
					ReleaseId: ReleaseId{
						SourceRevision:   "abc123def456",
						PipelineRevision: "789ghi012jkl",
					},
					Branch:           "main",
					Timestamp:        "2025-11-05T10:00:00Z",
					PullRequestID:    12345678,
					ServiceGroup:     "Microsoft.Azure.ARO.HCP.Global",
					ServiceGroupBase: "Microsoft.Azure.ARO.HCP",
				},
				Target: DeploymentTarget{
					Cloud:         "public",
					Environment:   "stg",
					RegionConfigs: []string{"eastus", "westus", "northeurope"},
				},
				Components: make(Components),
			},
			wantErr: false,
		},
		{
			name:    "old format with no RegionConfigs",
			fixture: "testdata/release_no_regions.yaml",
			want: &ReleaseDeployment{
				Metadata: ReleaseMetadata{
					ReleaseId: ReleaseId{
						SourceRevision:   "aaabbbccc",
						PipelineRevision: "111222333",
					},
					Branch:           "main",
					Timestamp:        "2025-11-05T11:00:00Z",
					PullRequestID:    11111111,
					ServiceGroup:     "Microsoft.Azure.ARO.HCP.Global",
					ServiceGroupBase: "Microsoft.Azure.ARO.HCP",
				},
				Target: DeploymentTarget{
					Cloud:         "fairfax",
					Environment:   "prod",
					RegionConfigs: nil,
				},
				Components: make(Components),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.fixture)
			if err != nil {
				t.Fatalf("failed to read fixture %s: %v", tt.fixture, err)
			}

			var got ReleaseDeployment
			err = yaml.Unmarshal(data, &got)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if diff := cmp.Diff(tt.want, &got); diff != "" {
				t.Errorf("UnmarshalYAML() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReleaseId_String(t *testing.T) {
	tests := []struct {
		name      string
		releaseId ReleaseId
		want      string
	}{
		{
			name: "basic format",
			releaseId: ReleaseId{
				SourceRevision:   "abc123",
				PipelineRevision: "def456",
			},
			want: "abc123-def456",
		},
		{
			name: "long hashes",
			releaseId: ReleaseId{
				SourceRevision:   "43697e5fa59a1234567890",
				PipelineRevision: "000779a4abcdef",
			},
			want: "43697e5fa59a1234567890-000779a4abcdef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.releaseId.String()
			if got != tt.want {
				t.Errorf("ReleaseId.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Helper function to create test fixtures if they don't exist
func init() {
	// This will be used by developers to generate test fixtures
	// Can be removed after fixtures are committed
	testdataDir := "testdata"
	os.MkdirAll(testdataDir, 0755)
}
