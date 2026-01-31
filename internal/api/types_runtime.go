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

package api

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HCPOpenShiftCluster implements runtime.Object and metav1.ObjectMetaAccessor
// for use with Kubernetes SharedInformer machinery.

var (
	_ runtime.Object            = &HCPOpenShiftCluster{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftCluster{}
)

func (o *HCPOpenShiftCluster) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftCluster) DeepCopyObject() runtime.Object {
	if o == nil {
		return nil
	}
	out := &HCPOpenShiftCluster{}

	// TrackedResource.Resource
	out.ID = deepCopyResourceID(o.ID)
	out.Name = o.Name
	out.Type = o.Type
	out.SystemData = deepCopySystemData(o.SystemData)

	// TrackedResource
	out.Location = o.Location
	out.Tags = maps.Clone(o.Tags)

	// CustomerProperties.Version
	out.CustomerProperties.Version.ID = o.CustomerProperties.Version.ID
	out.CustomerProperties.Version.ChannelGroup = o.CustomerProperties.Version.ChannelGroup

	// CustomerProperties.DNS
	out.CustomerProperties.DNS.BaseDomainPrefix = o.CustomerProperties.DNS.BaseDomainPrefix

	// CustomerProperties.Network
	out.CustomerProperties.Network.NetworkType = o.CustomerProperties.Network.NetworkType
	out.CustomerProperties.Network.PodCIDR = o.CustomerProperties.Network.PodCIDR
	out.CustomerProperties.Network.ServiceCIDR = o.CustomerProperties.Network.ServiceCIDR
	out.CustomerProperties.Network.MachineCIDR = o.CustomerProperties.Network.MachineCIDR
	out.CustomerProperties.Network.HostPrefix = o.CustomerProperties.Network.HostPrefix

	// CustomerProperties.API
	out.CustomerProperties.API.Visibility = o.CustomerProperties.API.Visibility
	out.CustomerProperties.API.AuthorizedCIDRs = slices.Clone(o.CustomerProperties.API.AuthorizedCIDRs)

	// CustomerProperties.Platform
	out.CustomerProperties.Platform.ManagedResourceGroup = o.CustomerProperties.Platform.ManagedResourceGroup
	out.CustomerProperties.Platform.SubnetID = deepCopyResourceID(o.CustomerProperties.Platform.SubnetID)
	out.CustomerProperties.Platform.OutboundType = o.CustomerProperties.Platform.OutboundType
	out.CustomerProperties.Platform.NetworkSecurityGroupID = deepCopyResourceID(o.CustomerProperties.Platform.NetworkSecurityGroupID)
	out.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators =
		deepCopyResourceIDMap(o.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators)
	out.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators =
		deepCopyResourceIDMap(o.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators)
	out.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity =
		deepCopyResourceID(o.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity)

	// CustomerProperties.Autoscaling
	out.CustomerProperties.Autoscaling.MaxNodesTotal = o.CustomerProperties.Autoscaling.MaxNodesTotal
	out.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds = o.CustomerProperties.Autoscaling.MaxPodGracePeriodSeconds
	out.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds = o.CustomerProperties.Autoscaling.MaxNodeProvisionTimeSeconds
	out.CustomerProperties.Autoscaling.PodPriorityThreshold = o.CustomerProperties.Autoscaling.PodPriorityThreshold

	// CustomerProperties.NodeDrainTimeoutMinutes
	out.CustomerProperties.NodeDrainTimeoutMinutes = o.CustomerProperties.NodeDrainTimeoutMinutes

	// CustomerProperties.Etcd
	out.CustomerProperties.Etcd.DataEncryption.KeyManagementMode = o.CustomerProperties.Etcd.DataEncryption.KeyManagementMode
	if o.CustomerProperties.Etcd.DataEncryption.CustomerManaged != nil {
		cm := &CustomerManagedEncryptionProfile{}
		cm.EncryptionType = o.CustomerProperties.Etcd.DataEncryption.CustomerManaged.EncryptionType
		if o.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms != nil {
			cm.Kms = &KmsEncryptionProfile{
				ActiveKey: KmsKey{
					Name:      o.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Name,
					VaultName: o.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.VaultName,
					Version:   o.CustomerProperties.Etcd.DataEncryption.CustomerManaged.Kms.ActiveKey.Version,
				},
			}
		}
		out.CustomerProperties.Etcd.DataEncryption.CustomerManaged = cm
	}

	// CustomerProperties.ClusterImageRegistry
	out.CustomerProperties.ClusterImageRegistry.State = o.CustomerProperties.ClusterImageRegistry.State

	// ServiceProviderProperties
	out.ServiceProviderProperties.ProvisioningState = o.ServiceProviderProperties.ProvisioningState
	out.ServiceProviderProperties.ClusterServiceID = o.ServiceProviderProperties.ClusterServiceID
	out.ServiceProviderProperties.ActiveOperationID = o.ServiceProviderProperties.ActiveOperationID
	out.ServiceProviderProperties.DNS.BaseDomain = o.ServiceProviderProperties.DNS.BaseDomain
	out.ServiceProviderProperties.Console.URL = o.ServiceProviderProperties.Console.URL
	out.ServiceProviderProperties.API.URL = o.ServiceProviderProperties.API.URL
	out.ServiceProviderProperties.Platform.IssuerURL = o.ServiceProviderProperties.Platform.IssuerURL

	// Identity
	out.Identity = deepCopyManagedServiceIdentity(o.Identity)

	return out
}

func (o *HCPOpenShiftCluster) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

// HCPOpenShiftClusterList is a list of HCPOpenShiftClusters compatible with
// runtime.Object for use with Kubernetes informer machinery.
type HCPOpenShiftClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HCPOpenShiftCluster `json:"items"`
}

var _ runtime.Object = &HCPOpenShiftClusterList{}

func (l *HCPOpenShiftClusterList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

func (l *HCPOpenShiftClusterList) DeepCopyObject() runtime.Object {
	if l == nil {
		return nil
	}
	out := *l
	if l.Items != nil {
		out.Items = make([]HCPOpenShiftCluster, len(l.Items))
		for i := range l.Items {
			copied := l.Items[i].DeepCopyObject().(*HCPOpenShiftCluster)
			out.Items[i] = *copied
		}
	}
	return &out
}

// HCPOpenShiftClusterNodePool implements runtime.Object and
// metav1.ObjectMetaAccessor for use with Kubernetes SharedInformer machinery.

var (
	_ runtime.Object            = &HCPOpenShiftClusterNodePool{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftClusterNodePool{}
)

func (o *HCPOpenShiftClusterNodePool) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftClusterNodePool) DeepCopyObject() runtime.Object {
	if o == nil {
		return nil
	}
	out := &HCPOpenShiftClusterNodePool{}

	// TrackedResource.Resource
	out.ID = deepCopyResourceID(o.ID)
	out.Name = o.Name
	out.Type = o.Type
	out.SystemData = deepCopySystemData(o.SystemData)

	// TrackedResource
	out.Location = o.Location
	out.Tags = maps.Clone(o.Tags)

	// Properties
	out.Properties.ProvisioningState = o.Properties.ProvisioningState
	out.Properties.Version.ID = o.Properties.Version.ID
	out.Properties.Version.ChannelGroup = o.Properties.Version.ChannelGroup
	out.Properties.Platform.SubnetID = deepCopyResourceID(o.Properties.Platform.SubnetID)
	out.Properties.Platform.VMSize = o.Properties.Platform.VMSize
	out.Properties.Platform.EnableEncryptionAtHost = o.Properties.Platform.EnableEncryptionAtHost
	out.Properties.Platform.OSDisk.SizeGiB = deepCopyPtr(o.Properties.Platform.OSDisk.SizeGiB)
	out.Properties.Platform.OSDisk.DiskStorageAccountType = o.Properties.Platform.OSDisk.DiskStorageAccountType
	out.Properties.Platform.OSDisk.EncryptionSetID = deepCopyResourceID(o.Properties.Platform.OSDisk.EncryptionSetID)
	out.Properties.Platform.AvailabilityZone = o.Properties.Platform.AvailabilityZone
	out.Properties.Replicas = o.Properties.Replicas
	out.Properties.AutoRepair = o.Properties.AutoRepair
	out.Properties.AutoScaling = deepCopyPtr(o.Properties.AutoScaling)
	out.Properties.Labels = maps.Clone(o.Properties.Labels)
	out.Properties.Taints = slices.Clone(o.Properties.Taints)
	out.Properties.NodeDrainTimeoutMinutes = deepCopyPtr(o.Properties.NodeDrainTimeoutMinutes)

	// ServiceProviderProperties
	out.ServiceProviderProperties.ClusterServiceID = o.ServiceProviderProperties.ClusterServiceID
	out.ServiceProviderProperties.ActiveOperationID = o.ServiceProviderProperties.ActiveOperationID

	// Identity
	out.Identity = deepCopyManagedServiceIdentity(o.Identity)

	return out
}

func (o *HCPOpenShiftClusterNodePool) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

// HCPOpenShiftClusterNodePoolList is a list of HCPOpenShiftClusterNodePools
// compatible with runtime.Object for use with Kubernetes informer machinery.
type HCPOpenShiftClusterNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HCPOpenShiftClusterNodePool `json:"items"`
}

var _ runtime.Object = &HCPOpenShiftClusterNodePoolList{}

func (l *HCPOpenShiftClusterNodePoolList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

func (l *HCPOpenShiftClusterNodePoolList) DeepCopyObject() runtime.Object {
	if l == nil {
		return nil
	}
	out := *l
	if l.Items != nil {
		out.Items = make([]HCPOpenShiftClusterNodePool, len(l.Items))
		for i := range l.Items {
			copied := l.Items[i].DeepCopyObject().(*HCPOpenShiftClusterNodePool)
			out.Items[i] = *copied
		}
	}
	return &out
}

// Operation implements runtime.Object and metav1.ObjectMetaAccessor for use
// with Kubernetes SharedInformer machinery.

var (
	_ runtime.Object            = &Operation{}
	_ metav1.ObjectMetaAccessor = &Operation{}
)

func (o *Operation) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *Operation) DeepCopyObject() runtime.Object {
	if o == nil {
		return nil
	}
	out := &Operation{}

	out.ResourceID = deepCopyResourceID(o.ResourceID)
	out.TenantID = o.TenantID
	out.ClientID = o.ClientID
	out.Request = o.Request
	out.ExternalID = deepCopyResourceID(o.ExternalID)
	out.InternalID = o.InternalID
	out.OperationID = deepCopyResourceID(o.OperationID)
	out.ClientRequestID = o.ClientRequestID
	out.CorrelationRequestID = o.CorrelationRequestID
	out.NotificationURI = o.NotificationURI
	out.StartTime = o.StartTime
	out.LastTransitionTime = o.LastTransitionTime
	out.Status = o.Status
	out.Error = deepCopyCloudErrorBody(o.Error)

	return out
}

func (o *Operation) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ResourceID != nil {
		om.Name = strings.ToLower(o.ResourceID.String())
	}
	return om
}

// OperationList is a list of Operations compatible with runtime.Object for use
// with Kubernetes informer machinery.
type OperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Operation `json:"items"`
}

var _ runtime.Object = &OperationList{}

func (l *OperationList) GetObjectKind() schema.ObjectKind {
	return &l.TypeMeta
}

func (l *OperationList) DeepCopyObject() runtime.Object {
	if l == nil {
		return nil
	}
	out := *l
	if l.Items != nil {
		out.Items = make([]Operation, len(l.Items))
		for i := range l.Items {
			copied := l.Items[i].DeepCopyObject().(*Operation)
			out.Items[i] = *copied
		}
	}
	return &out
}

// Deep copy helpers

func deepCopyResourceID(id *azcorearm.ResourceID) *azcorearm.ResourceID {
	if id == nil {
		return nil
	}
	s := id.String()
	if s == "" {
		// Zero-value ResourceID: allocate a new one so we don't share the pointer.
		return new(azcorearm.ResourceID)
	}
	// ResourceID has unexported fields, so we reconstruct from its string form.
	copied, err := azcorearm.ParseResourceID(s)
	if err != nil {
		panic(fmt.Sprintf("deepCopyResourceID: failed to parse %q: %v", s, err))
	}
	return copied
}

func deepCopyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func deepCopyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func deepCopyStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

func deepCopySystemData(sd *arm.SystemData) *arm.SystemData {
	if sd == nil {
		return nil
	}
	out := *sd
	out.CreatedAt = deepCopyTimePtr(sd.CreatedAt)
	out.LastModifiedAt = deepCopyTimePtr(sd.LastModifiedAt)
	return &out
}

func deepCopyResourceIDMap(m map[string]*azcorearm.ResourceID) map[string]*azcorearm.ResourceID {
	if m == nil {
		return nil
	}
	out := make(map[string]*azcorearm.ResourceID, len(m))
	for k, v := range m {
		out[k] = deepCopyResourceID(v)
	}
	return out
}

func deepCopyManagedServiceIdentity(id *arm.ManagedServiceIdentity) *arm.ManagedServiceIdentity {
	if id == nil {
		return nil
	}
	out := *id
	if id.UserAssignedIdentities != nil {
		out.UserAssignedIdentities = make(map[string]*arm.UserAssignedIdentity, len(id.UserAssignedIdentities))
		for k, v := range id.UserAssignedIdentities {
			if v != nil {
				uai := *v
				uai.ClientID = deepCopyStringPtr(v.ClientID)
				uai.PrincipalID = deepCopyStringPtr(v.PrincipalID)
				out.UserAssignedIdentities[k] = &uai
			} else {
				out.UserAssignedIdentities[k] = nil
			}
		}
	}
	return &out
}

func deepCopyCloudErrorBody(e *arm.CloudErrorBody) *arm.CloudErrorBody {
	if e == nil {
		return nil
	}
	out := *e
	if e.Details != nil {
		out.Details = make([]arm.CloudErrorBody, len(e.Details))
		for i := range e.Details {
			out.Details[i] = *deepCopyCloudErrorBody(&e.Details[i])
		}
	}
	return &out
}
