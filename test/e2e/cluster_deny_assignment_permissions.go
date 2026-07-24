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

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armauthorization "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Deny Assignment Permissions", func() {
	BeforeEach(func() {
		// do nothing.  per test initialization usually ages better than shared.
	})

	It("should allow all expected customer permissions on the managed resource group",
		labels.RequireNothing,
		labels.Critical,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerNsgName        = "customer-nsg"
				customerVnetName       = "customer-vnet"
				customerVnetSubnetName = "customer-vnet-subnet1"
				customerClusterName    = "deny-perm"
				customerNodePoolName   = "deny-np"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "deny-perm", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources (infrastructure and managed identities)")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{
					"customerNsgName":        customerNsgName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for cluster %q", customerClusterName)

			By("creating the HCP cluster and waiting for it to succeed")
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %q", customerClusterName)

			By("creating a nodepool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = customerNodePoolName
			nodePoolParams.Replicas = int32(2)
			err = tc.CreateNodePoolFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				managedResourceGroupName,
				customerClusterName,
				nodePoolParams,
				framework.NodePoolCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create nodepool %q for cluster %q", customerNodePoolName, customerClusterName)

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription id for cluster %q", customerClusterName)

			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential %q", customerClusterName)

			// --- Permission scenarios ---

			By("verifying disk write, beginGetAccess, and endGetAccess")
			diskName := "e2e-deny-test-disk"
			disksClient := tc.GetARMComputeClientFactoryOrDie(ctx).NewDisksClient()

			By("creating a managed disk (disks/write)")
			diskPoller, err := disksClient.BeginCreateOrUpdate(ctx, managedResourceGroupName, diskName, armcompute.Disk{
				Location: to.Ptr(tc.Location()),
				Properties: &armcompute.DiskProperties{
					CreationData: &armcompute.CreationData{
						CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty),
					},
					DiskSizeGB: to.Ptr[int32](100),
				},
				SKU: &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesStandardLRS)},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin disk creation in managed resource group %q", managedResourceGroupName)
			_, err = diskPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete disk creation in managed resource group %q", managedResourceGroupName)

			By("granting read access to the disk (disks/beginGetAccess)")
			diskAccessPoller, err := disksClient.BeginGrantAccess(ctx, managedResourceGroupName, diskName, armcompute.GrantAccessData{
				Access:            to.Ptr(armcompute.AccessLevelRead),
				DurationInSeconds: to.Ptr[int32](3600),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin granting disk access in managed resource group %q", managedResourceGroupName)
			_, err = diskAccessPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete granting disk access in managed resource group %q", managedResourceGroupName)

			By("revoking access to the disk (disks/endGetAccess)")
			diskRevokePoller, err := disksClient.BeginRevokeAccess(ctx, managedResourceGroupName, diskName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin revoking disk access in managed resource group %q", managedResourceGroupName)
			_, err = diskRevokePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete revoking disk access in managed resource group %q", managedResourceGroupName)

			By("verifying snapshot write, beginGetAccess, endGetAccess, and delete")
			snapshotName := "e2e-deny-test-snap"
			computeFactory := tc.GetARMComputeClientFactoryOrDie(ctx)
			snapshotsClient := computeFactory.NewSnapshotsClient()

			By("finding an OS disk in the managed resource group to snapshot")
			vms, err := framework.GetVirtualMachinesInResourceGroup(ctx, computeFactory, managedResourceGroupName, 1)
			Expect(err).NotTo(HaveOccurred(), "failed to list VMs in managed resource group %q", managedResourceGroupName)
			Expect(vms[0].Name).NotTo(BeNil(), "VM Name is nil")
			Expect(vms[0].Properties).NotTo(BeNil(), "VM Properties is nil")
			Expect(vms[0].Properties.StorageProfile).NotTo(BeNil(), "VM StorageProfile is nil")
			Expect(vms[0].Properties.StorageProfile.OSDisk).NotTo(BeNil(), "VM OSDisk is nil")
			Expect(vms[0].Properties.StorageProfile.OSDisk.ManagedDisk).NotTo(BeNil(), "VM OSDisk ManagedDisk is nil")
			osDiskID := vms[0].Properties.StorageProfile.OSDisk.ManagedDisk.ID
			Expect(osDiskID).NotTo(BeNil(), "VM OSDisk ManagedDisk ID is nil")

			By("creating a snapshot from the OS disk (snapshots/write)")
			snapPoller, err := snapshotsClient.BeginCreateOrUpdate(ctx, managedResourceGroupName, snapshotName, armcompute.Snapshot{
				Location: to.Ptr(tc.Location()),
				Properties: &armcompute.SnapshotProperties{
					CreationData: &armcompute.CreationData{
						CreateOption:     to.Ptr(armcompute.DiskCreateOptionCopy),
						SourceResourceID: osDiskID,
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin snapshot creation in managed resource group %q", managedResourceGroupName)
			_, err = snapPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete snapshot creation in managed resource group %q", managedResourceGroupName)

			By("granting read access to the snapshot (snapshots/beginGetAccess)")
			snapAccessPoller, err := snapshotsClient.BeginGrantAccess(ctx, managedResourceGroupName, snapshotName, armcompute.GrantAccessData{
				Access:            to.Ptr(armcompute.AccessLevelRead),
				DurationInSeconds: to.Ptr[int32](3600),
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin granting snapshot access in managed resource group %q", managedResourceGroupName)
			_, err = snapAccessPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete granting snapshot access in managed resource group %q", managedResourceGroupName)

			By("revoking access to the snapshot (snapshots/endGetAccess)")
			snapRevokePoller, err := snapshotsClient.BeginRevokeAccess(ctx, managedResourceGroupName, snapshotName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin revoking snapshot access in managed resource group %q", managedResourceGroupName)
			_, err = snapRevokePoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete revoking snapshot access in managed resource group %q", managedResourceGroupName)

			By("deleting the snapshot (snapshots/delete)")
			snapDelPoller, err := snapshotsClient.BeginDelete(ctx, managedResourceGroupName, snapshotName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin snapshot deletion in managed resource group %q", managedResourceGroupName)
			_, err = snapDelPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete snapshot deletion in managed resource group %q", managedResourceGroupName)

			By("verifying VM retrieveBootDiagnosticsData")
			vmClient := computeFactory.NewVirtualMachinesClient()
			By("retrieving boot diagnostics data (virtualMachines/retrieveBootDiagnosticsData)")
			_, err = vmClient.RetrieveBootDiagnosticsData(ctx, managedResourceGroupName, *vms[0].Name, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to retrieve boot diagnostics data for VM %q in managed resource group %q", *vms[0].Name, managedResourceGroupName)

			By("verifying ActionGroups write and delete")
			agName := "e2e-deny-test-ag"
			agClient, err := armmonitor.NewActionGroupsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create action groups client")

			By("creating an action group (ActionGroups/write)")
			_, err = agClient.CreateOrUpdate(ctx, managedResourceGroupName, agName, armmonitor.ActionGroupResource{
				Location: to.Ptr("Global"),
				Properties: &armmonitor.ActionGroup{
					Enabled:        to.Ptr(false),
					GroupShortName: to.Ptr("e2edenytst"),
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create action group in managed resource group %q", managedResourceGroupName)

			By("deleting the action group (ActionGroups/delete)")
			_, err = agClient.Delete(ctx, managedResourceGroupName, agName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete action group in managed resource group %q", managedResourceGroupName)

			By("verifying MetricAlerts write and delete")
			alertName := "e2e-deny-test-metric-alert"
			maClient, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create metric alerts client")

			Expect(vms[0].ID).NotTo(BeNil(), "VM ID is nil for metric alert scope")

			By("creating a metric alert (MetricAlerts/write)")
			_, err = maClient.CreateOrUpdate(ctx, managedResourceGroupName, alertName, armmonitor.MetricAlertResource{
				Location: to.Ptr("global"),
				Properties: &armmonitor.MetricAlertProperties{
					Enabled:             to.Ptr(false),
					Severity:            to.Ptr[int32](4),
					EvaluationFrequency: to.Ptr("PT5M"),
					WindowSize:          to.Ptr("PT5M"),
					Scopes:              []*string{vms[0].ID},
					Criteria: &armmonitor.MetricAlertSingleResourceMultipleMetricCriteria{
						ODataType: to.Ptr(armmonitor.OdatatypeMicrosoftAzureMonitorSingleResourceMultipleMetricCriteria),
						AllOf: []*armmonitor.MetricCriteria{
							{
								CriterionType:   to.Ptr(armmonitor.CriterionTypeStaticThresholdCriterion),
								Name:            to.Ptr("cpu-check"),
								MetricName:      to.Ptr("Percentage CPU"),
								MetricNamespace: to.Ptr("Microsoft.Compute/virtualMachines"),
								Operator:        to.Ptr(armmonitor.OperatorGreaterThan),
								Threshold:       to.Ptr[float64](90),
								TimeAggregation: to.Ptr(armmonitor.AggregationTypeEnumAverage),
							},
						},
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create metric alert in managed resource group %q", managedResourceGroupName)

			By("deleting the metric alert (MetricAlerts/delete)")
			_, err = maClient.Delete(ctx, managedResourceGroupName, alertName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete metric alert in managed resource group %q", managedResourceGroupName)

			By("verifying ActivityLogAlerts write and delete")
			activityAlertName := "e2e-deny-test-activity-alert"
			alaClient, err := armmonitor.NewActivityLogAlertsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create activity log alerts client")

			By("creating an activity log alert (ActivityLogAlerts/write)")
			mrgResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedResourceGroupName)
			_, err = alaClient.CreateOrUpdate(ctx, managedResourceGroupName, activityAlertName, armmonitor.ActivityLogAlertResource{
				Location: to.Ptr("Global"),
				Properties: &armmonitor.AlertRuleProperties{
					Enabled: to.Ptr(false),
					Scopes:  []*string{to.Ptr(mrgResourceID)},
					Condition: &armmonitor.AlertRuleAllOfCondition{
						AllOf: []*armmonitor.AlertRuleAnyOfOrLeafCondition{
							{
								Field:  to.Ptr("category"),
								Equals: to.Ptr("Administrative"),
							},
						},
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create activity log alert in managed resource group %q", managedResourceGroupName)

			By("deleting the activity log alert (ActivityLogAlerts/delete)")
			_, err = alaClient.Delete(ctx, managedResourceGroupName, activityAlertName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete activity log alert in managed resource group %q", managedResourceGroupName)

			By("verifying tags write and delete on the test disk")
			tagsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewTagsClient()
			diskResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/disks/%s", subscriptionID, managedResourceGroupName, diskName)

			By("writing a tag on the test disk (tags/write)")
			_, err = tagsClient.CreateOrUpdateAtScope(ctx, diskResourceID, armresources.TagsResource{
				Properties: &armresources.Tags{
					Tags: map[string]*string{
						"e2e-deny-test": to.Ptr("test-value"),
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to write tag on test disk in managed resource group %q", managedResourceGroupName)

			By("deleting the tag from the test disk (tags/delete)")
			_, err = tagsClient.DeleteAtScope(ctx, diskResourceID, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete tag on test disk in managed resource group %q", managedResourceGroupName)

			// roleAssignments/delete is intentionally not permitted (customers could remove
			// service-managed RBAC). The Reader role assignment created here is cleaned up
			// when the managed resource group is deleted during test teardown.
			By("verifying roleAssignments write")
			raClient, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create role assignments client")

			By("looking up the current identity's object ID")
			identity, err := tc.GetCurrentAzureIdentityDetails(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get current Azure identity details")

			By("creating a role assignment on the managed resource group (roleAssignments/write)")
			mrgScope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, managedResourceGroupName)
			// Built-in "Reader" role definition
			readerRoleDefID := fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/acdd72a7-3385-48ef-bd42-f606fba81ae7", subscriptionID)
			raName := uuid.New().String()

			_, err = raClient.Create(ctx, mrgScope, raName, armauthorization.RoleAssignmentCreateParameters{
				Properties: &armauthorization.RoleAssignmentProperties{
					PrincipalID:      to.Ptr(identity.ObjectID),
					RoleDefinitionID: to.Ptr(readerRoleDefID),
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create role assignment on managed resource group %q", managedResourceGroupName)

			By("finding a DNS zone in the managed resource group")
			zoneClient, err := armdns.NewZonesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create DNS zone client")

			var zoneName string
			zonePager := zoneClient.NewListByResourceGroupPager(managedResourceGroupName, nil)
			for zonePager.More() {
				page, err := zonePager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to list DNS zones in managed resource group %q", managedResourceGroupName)
				if len(page.Value) > 0 && page.Value[0].Name != nil {
					zoneName = *page.Value[0].Name
					break
				}
			}
			Expect(zoneName).NotTo(BeEmpty(), "no DNS zones found in managed resource group %q", managedResourceGroupName)

			recordSetsClient, err := armdns.NewRecordSetsClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create DNS record sets client")

			By("writing a CAA record (dnszones/CAA/write)")
			_, err = recordSetsClient.CreateOrUpdate(ctx, managedResourceGroupName, zoneName, "e2e-deny-test-caa", armdns.RecordTypeCAA, armdns.RecordSet{
				Properties: &armdns.RecordSetProperties{
					TTL: to.Ptr[int64](300),
					CaaRecords: []*armdns.CaaRecord{
						{Flags: to.Ptr[int32](0), Tag: to.Ptr("issue"), Value: to.Ptr("letsencrypt.org")},
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to write CAA record on zone %q in managed resource group %q", zoneName, managedResourceGroupName)

			By("writing a TXT record (dnszones/TXT/write)")
			_, err = recordSetsClient.CreateOrUpdate(ctx, managedResourceGroupName, zoneName, "e2e-deny-test-txt", armdns.RecordTypeTXT, armdns.RecordSet{
				Properties: &armdns.RecordSetProperties{
					TTL: to.Ptr[int64](300),
					TxtRecords: []*armdns.TxtRecord{
						{Value: []*string{to.Ptr("v=spf1 e2e-test")}},
					},
				},
			}, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to write TXT record on zone %q in managed resource group %q", zoneName, managedResourceGroupName)

			By("deleting the CAA record (dnszones/CAA/delete)")
			_, err = recordSetsClient.Delete(ctx, managedResourceGroupName, zoneName, "e2e-deny-test-caa", armdns.RecordTypeCAA, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete CAA record on zone %q in managed resource group %q", zoneName, managedResourceGroupName)

			By("deleting the TXT record (dnszones/TXT/delete)")
			_, err = recordSetsClient.Delete(ctx, managedResourceGroupName, zoneName, "e2e-deny-test-txt", armdns.RecordTypeTXT, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to delete TXT record on zone %q in managed resource group %q", zoneName, managedResourceGroupName)

			By("verifying NIC effectiveRouteTable action")
			nicsClient, err := armnetwork.NewInterfacesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create network interfaces client")

			By("finding a NIC in the managed resource group")
			var nicName string
			nicPager := nicsClient.NewListPager(managedResourceGroupName, nil)
			for nicPager.More() {
				page, err := nicPager.NextPage(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to list NICs in managed resource group %q", managedResourceGroupName)
				if len(page.Value) > 0 && page.Value[0].Name != nil {
					nicName = *page.Value[0].Name
					break
				}
			}
			Expect(nicName).NotTo(BeEmpty(), "no NICs found in managed resource group %q", managedResourceGroupName)

			By("getting the effective route table (networkInterfaces/effectiveRouteTable)")
			ertPoller, err := nicsClient.BeginGetEffectiveRouteTable(ctx, managedResourceGroupName, nicName, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to begin getting effective route table for NIC %q in managed resource group %q", nicName, managedResourceGroupName)
			_, err = ertPoller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Second})
			Expect(err).NotTo(HaveOccurred(), "failed to complete getting effective route table for NIC %q in managed resource group %q", nicName, managedResourceGroupName)

			// NSG join action: not testable today because the NSG is customer-brought
			// and lives in the customer resource group. join/action on the managed
			// resource group is not exercisable.

			// Policy remediations write and delete: not testable today because
			// policyAssignments/write is not in the deny assignment NotActions,
			// so we can't create the prerequisite policy assignment needed to
			// test remediations.
		})
})
