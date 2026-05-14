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

package kubeapplier

// ManagementClusterAccessor is implemented by every *Desire so that the database
// layer can read the partition-key value (the management cluster name) without
// importing this package's concrete types.
type ManagementClusterAccessor interface {
	GetManagementCluster() string
}

func (d *ApplyDesire) GetManagementCluster() string  { return d.Spec.ManagementCluster }
func (d *DeleteDesire) GetManagementCluster() string { return d.Spec.ManagementCluster }
func (d *ReadDesire) GetManagementCluster() string   { return d.Spec.ManagementCluster }
