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

package cosmosstorageutils

import (
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestNodePoolEnsureDefaults(t *testing.T) {
	tests := []struct {
		name                       string
		diskStorageAccountType     metadataapi.DiskStorageAccountType
		wantDiskStorageAccountType metadataapi.DiskStorageAccountType
		diskType                   metadataapi.OsDiskType
		wantDiskType               metadataapi.OsDiskType
	}{
		{
			name:                       "zero values get defaults",
			diskStorageAccountType:     "",
			wantDiskStorageAccountType: metadataapi.DiskStorageAccountTypePremium_LRS,
			diskType:                   "",
			wantDiskType:               metadataapi.OsDiskTypeManaged,
		},
		{
			name:                       "explicit Premium_LRS preserved",
			diskStorageAccountType:     metadataapi.DiskStorageAccountTypePremium_LRS,
			wantDiskStorageAccountType: metadataapi.DiskStorageAccountTypePremium_LRS,
			diskType:                   metadataapi.OsDiskTypeManaged,
			wantDiskType:               metadataapi.OsDiskTypeManaged,
		},
		{
			name:                       "explicit StandardSSD_LRS preserved",
			diskStorageAccountType:     metadataapi.DiskStorageAccountTypeStandardSSD_LRS,
			wantDiskStorageAccountType: metadataapi.DiskStorageAccountTypeStandardSSD_LRS,
			diskType:                   metadataapi.OsDiskTypeEphemeral,
			wantDiskType:               metadataapi.OsDiskTypeEphemeral,
		},
		{
			name:                       "explicit Standard_LRS preserved",
			diskStorageAccountType:     metadataapi.DiskStorageAccountTypeStandard_LRS,
			wantDiskStorageAccountType: metadataapi.DiskStorageAccountTypeStandard_LRS,
			diskType:                   metadataapi.OsDiskTypeManaged,
			wantDiskType:               metadataapi.OsDiskTypeManaged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			np := &coreapi.HCPOpenShiftClusterNodePool{}
			np.Properties.Platform.OSDisk.DiskStorageAccountType = tt.diskStorageAccountType
			np.Properties.Platform.OSDisk.DiskType = tt.diskType

			np.EnsureDefaults()

			if np.Properties.Platform.OSDisk.DiskStorageAccountType != tt.wantDiskStorageAccountType {
				t.Errorf("DiskStorageAccountType = %q, want %q",
					np.Properties.Platform.OSDisk.DiskStorageAccountType,
					tt.wantDiskStorageAccountType)
			}
			if np.Properties.Platform.OSDisk.DiskType != tt.wantDiskType {
				t.Errorf("DiskType = %q, want %q",
					np.Properties.Platform.OSDisk.DiskType,
					tt.wantDiskType)
			}
		})
	}
}
