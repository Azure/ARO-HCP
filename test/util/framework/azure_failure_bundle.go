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

package framework

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
)

type azureFailureOperation struct {
	index         int
	ResourceGroup string    `json:"resourceGroup,omitempty"`
	Operation     string    `json:"operation"`
	Resource      string    `json:"resource,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	Status        string    `json:"status"`
	Count         int       `json:"count"`
	Artifacts     []string  `json:"artifacts,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type azureFailureManifest struct {
	Cluster               string                  `json:"cluster"`
	NodePool              string                  `json:"nodePool"`
	CustomerResourceGroup string                  `json:"customerResourceGroup"`
	ManagedResourceGroup  string                  `json:"managedResourceGroup"`
	StartedAt             time.Time               `json:"startedAt"`
	FinishedAt            time.Time               `json:"finishedAt"`
	Deadline              time.Time               `json:"deadline"`
	Operations            []azureFailureOperation `json:"operations"`
}

// The lock protects only manifest updates, never Azure requests or artifact writes.
type azureFailureBundle struct {
	directory       string
	manifest        azureFailureManifest
	lock            sync.Mutex
	errors          []error
	effectiveClient *azcorearm.Client
	subscriptionID  string
}

func (tc *perItOrDescribeTestContext) collectNodePoolFailureArtifacts(ctx context.Context, logger logr.Logger, customerRG, managedRG, cluster, nodePool string) {
	if tc.LogDirPath == "" {
		logger.Info("skipping Azure failure bundle: no artifact directory configured")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	bundle, err := newAzureFailureBundle(ctx, tc.LogDirPath, customerRG, managedRG, cluster, nodePool)
	if err != nil {
		logger.Error(errors.New(azureFailureError(err)), "failed to create Azure failure bundle")
		return
	}
	logger.Info("collecting Azure node pool failure bundle", "directory", bundle.directory)

	// Resolve lazy test-context clients before starting any collection branches.
	var compute *armcompute.ClientFactory
	var network *armnetwork.ClientFactory
	var dns *armprivatedns.ClientFactory
	bundle.operation(ctx, "", "compute-client", "", func(op *azureFailureOperation) error {
		var err error
		compute, err = tc.GetARMComputeClientFactory(ctx)
		return err
	})
	bundle.operation(ctx, "", "network-client", "", func(op *azureFailureOperation) error {
		var err error
		network, err = tc.GetARMNetworkClientFactory(ctx)
		if err != nil {
			return err
		}
		credential, err := tc.AzureCredential()
		if err != nil {
			return err
		}
		bundle.subscriptionID, err = tc.SubscriptionID(ctx)
		if err != nil {
			return err
		}
		bundle.effectiveClient, err = azcorearm.NewClient("azure-failure-bundle", "v0.0.0", credential, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
		return err
	})
	bundle.operation(ctx, "", "private-dns-client", "", func(op *azureFailureOperation) error {
		subscription, err := tc.SubscriptionID(ctx)
		if err != nil {
			return err
		}
		credential, err := tc.AzureCredential()
		if err != nil {
			return err
		}
		dns, err = armprivatedns.NewClientFactory(subscription, credential, tc.perBinaryInvocationTestContext.getClientFactoryOptions())
		return err
	})
	if err := bundle.collect(ctx, compute, network, dns); err != nil {
		logger.Error(err, "Azure failure bundle is incomplete", "directory", bundle.directory)
	}
}

func newAzureFailureBundle(ctx context.Context, target, customerRG, managedRG, cluster, nodePool string) (*azureFailureBundle, error) {
	if err := os.MkdirAll(target, 0755); err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	directory, err := os.MkdirTemp(target, "azure-failure-"+azureArtifactName(nodePool)+"-"+started.Format("20060102T150405.000000000Z")+"-")
	if err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	b := &azureFailureBundle{directory: directory, manifest: azureFailureManifest{
		Cluster: cluster, NodePool: nodePool, CustomerResourceGroup: customerRG, ManagedResourceGroup: managedRG,
		StartedAt: started, Deadline: deadline, Operations: []azureFailureOperation{},
	}}
	return b, b.saveManifest()
}

func (b *azureFailureBundle) saveManifest() error {
	data, err := json.MarshalIndent(b.manifest, "", "  ")
	if err != nil {
		return err
	}
	// Readers should see either the previous complete manifest or the new one.
	temporary := filepath.Join(b.directory, "manifest.json.tmp")
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(b.directory, "manifest.json"))
}

func (b *azureFailureBundle) operation(ctx context.Context, rg, name, resource string, collect func(*azureFailureOperation) error) {
	op := azureFailureOperation{ResourceGroup: rg, Operation: name, Resource: resource, StartedAt: time.Now().UTC(), Status: "running"}
	b.lock.Lock()
	op.index = len(b.manifest.Operations)
	b.manifest.Operations = append(b.manifest.Operations, op)
	if err := b.saveManifest(); err != nil {
		b.errors = append(b.errors, fmt.Errorf("write manifest: %s", azureFailureError(err)))
	}
	b.lock.Unlock()
	var err error
	if ctx.Err() != nil {
		op.Status = "skipped-deadline"
		err = ctx.Err()
	} else {
		err = collect(&op)
		if err != nil {
			if op.Status != "incomplete" {
				op.Status = "error"
			}
		} else if op.Status == "running" {
			op.Status = "success"
		}
	}
	op.FinishedAt = time.Now().UTC()
	if err != nil {
		op.Error = azureFailureError(err)
	}
	b.lock.Lock()
	defer b.lock.Unlock()
	b.manifest.Operations[op.index] = op
	if err != nil {
		b.errors = append(b.errors, fmt.Errorf("%s/%s/%s: %s", rg, name, resource, op.Error))
	}
	if err := b.saveManifest(); err != nil {
		b.errors = append(b.errors, fmt.Errorf("write manifest: %s", azureFailureError(err)))
	}
}

func (b *azureFailureBundle) writeJSON(op *azureFailureOperation, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(azureArtifactName(op.ResourceGroup), azureArtifactName(op.Operation), azureArtifactName(op.Resource), name)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(b.directory, path)), 0700); err != nil {
		return err
	}
	// Redact decoded JSON strings so escaped quotes and newlines remain valid
	// JSON, and escaped characters cannot conceal a signed URL's query.
	data = azureJSONString.ReplaceAllFunc(data, func(encoded []byte) []byte {
		var value string
		if err := json.Unmarshal(encoded, &value); err != nil {
			return encoded
		}
		redacted, err := json.Marshal(redactAzureCredentialURLs(value))
		if err != nil {
			return encoded
		}
		return redacted
	})
	if err := os.WriteFile(filepath.Join(b.directory, path), data, 0600); err != nil {
		return err
	}
	op.Artifacts = append(op.Artifacts, path)
	b.lock.Lock()
	defer b.lock.Unlock()
	b.manifest.Operations[op.index] = *op
	return b.saveManifest()
}

// Keep SDK errors out of artifacts: ResponseError and URL errors can include
// response bodies, signed blob URLs, and request headers.
func azureFailureError(err error) string {
	if errors.Is(err, errEffectiveNetworkIncomplete) {
		return errEffectiveNetworkIncomplete.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "collection deadline exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "collection canceled"
	}
	var response *azcore.ResponseError
	if errors.As(err, &response) {
		code := response.ErrorCode
		if strings.IndexFunc(code, func(r rune) bool {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
				return false
			default:
				return true
			}
		}) >= 0 {
			code = "redacted"
		}
		return fmt.Sprintf("Azure HTTP %d (%s)", response.StatusCode, code)
	}
	return fmt.Sprintf("%T (details omitted to protect credentials)", err)
}

var azureCredentialURL = regexp.MustCompile(`(?i)https?://[^\s"<>]+`)
var azureJSONString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

func redactAzureCredentialURLs(value string) string {
	return azureCredentialURL.ReplaceAllStringFunc(value, func(candidate string) string {
		lower := strings.ToLower(candidate)
		for _, credential := range []string{"sig=", "sig%3d", "signature=", "token=", "secret=", "password=", "@"} {
			if strings.Contains(lower, credential) {
				return "[credential URL omitted]"
			}
		}
		return candidate
	})
}

func azureArtifactName(value string) string {
	value = strings.Trim(value, ".")
	if value == "" {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, value)
}

// Save each successful page immediately. The values callback can launch child
// reads before the next page, so pagination cannot starve discovered resources.
func collectAzurePages[P, T any](ctx context.Context, b *azureFailureBundle, rg, operation, resource string, pager *runtime.Pager[P], values func(P) []*T) []*T {
	var collected []*T
	b.operation(ctx, rg, operation, resource, func(op *azureFailureOperation) error {
		pageNumber := 0
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}
			items := values(page)
			if items == nil {
				items = []*T{}
			}
			collected = append(collected, items...)
			op.Count += len(items)
			pageNumber++
			if err := b.writeJSON(op, fmt.Sprintf("page-%04d.json", pageNumber), items); err != nil {
				return err
			}
		}
		if op.Count == 0 {
			op.Status = "empty"
		}
		return nil
	})
	return collected
}

func (b *azureFailureBundle) collect(ctx context.Context, compute *armcompute.ClientFactory, network *armnetwork.ClientFactory, dns *armprivatedns.ClientFactory) error {
	var branches sync.WaitGroup
	start := branches.Go
	groups := []string{b.manifest.CustomerResourceGroup}
	if !strings.EqualFold(b.manifest.CustomerResourceGroup, b.manifest.ManagedResourceGroup) {
		groups = append(groups, b.manifest.ManagedResourceGroup)
	}
	for _, rg := range groups {
		if rg == "" {
			b.operation(ctx, rg, "resource-group", "", func(op *azureFailureOperation) error {
				op.Status = "skipped-missing-resource-group"
				return nil
			})
			continue
		}
		if network != nil {
			start(func() {
				collectAzurePages(ctx, b, rg, "load-balancers", "", network.NewLoadBalancersClient().NewListPager(rg, nil), func(p armnetwork.LoadBalancersClientListResponse) []*armnetwork.LoadBalancer { return p.Value })
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "network-interfaces", "", network.NewInterfacesClient().NewListPager(rg, nil), func(p armnetwork.InterfacesClientListResponse) []*armnetwork.Interface {
					for _, nic := range p.Value {
						if nic == nil || nic.Name == nil || nic.Properties == nil || nic.Properties.VirtualMachine == nil || nic.Properties.VirtualMachine.ID == nil {
							continue
						}
						vm, err := azcorearm.ParseResourceID(*nic.Properties.VirtualMachine.ID)
						if err != nil || !strings.EqualFold(vm.ResourceGroupName, b.manifest.ManagedResourceGroup) {
							continue
						}
						start(func() { b.collectEffectiveNetwork(ctx, network, rg, *nic.Name) })
					}
					return p.Value
				})
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "virtual-networks", "", network.NewVirtualNetworksClient().NewListPager(rg, nil), func(p armnetwork.VirtualNetworksClientListResponse) []*armnetwork.VirtualNetwork {
					for _, vnet := range p.Value {
						if vnet != nil && vnet.Name != nil {
							start(func() {
								collectAzurePages(ctx, b, rg, "subnets", *vnet.Name, network.NewSubnetsClient().NewListPager(rg, *vnet.Name, nil), func(p armnetwork.SubnetsClientListResponse) []*armnetwork.Subnet { return p.Value })
							})
						}
					}
					return p.Value
				})
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "network-security-groups", "", network.NewSecurityGroupsClient().NewListPager(rg, nil), func(p armnetwork.SecurityGroupsClientListResponse) []*armnetwork.SecurityGroup { return p.Value })
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "route-tables", "", network.NewRouteTablesClient().NewListPager(rg, nil), func(p armnetwork.RouteTablesClientListResponse) []*armnetwork.RouteTable { return p.Value })
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "nat-gateways", "", network.NewNatGatewaysClient().NewListPager(rg, nil), func(p armnetwork.NatGatewaysClientListResponse) []*armnetwork.NatGateway { return p.Value })
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "public-ips", "", network.NewPublicIPAddressesClient().NewListPager(rg, nil), func(p armnetwork.PublicIPAddressesClientListResponse) []*armnetwork.PublicIPAddress { return p.Value })
			})
			start(func() {
				collectAzurePages(ctx, b, rg, "private-endpoints", "", network.NewPrivateEndpointsClient().NewListPager(rg, nil), func(p armnetwork.PrivateEndpointsClientListResponse) []*armnetwork.PrivateEndpoint {
					for _, endpoint := range p.Value {
						if endpoint != nil && endpoint.Name != nil {
							start(func() {
								collectAzurePages(ctx, b, rg, "private-dns-zone-groups", *endpoint.Name, network.NewPrivateDNSZoneGroupsClient().NewListPager(*endpoint.Name, rg, nil), func(p armnetwork.PrivateDNSZoneGroupsClientListResponse) []*armnetwork.PrivateDNSZoneGroup {
									return p.Value
								})
							})
						}
					}
					return p.Value
				})
			})
		} else {
			b.operation(ctx, rg, "networking", "", func(op *azureFailureOperation) error { op.Status = "skipped-client-unavailable"; return nil })
		}
		if dns != nil {
			start(func() {
				collectAzurePages(ctx, b, rg, "private-dns-zones", "", dns.NewPrivateZonesClient().NewListByResourceGroupPager(rg, nil), func(p armprivatedns.PrivateZonesClientListByResourceGroupResponse) []*armprivatedns.PrivateZone {
					for _, zone := range p.Value {
						if zone != nil && zone.Name != nil {
							start(func() {
								collectAzurePages(ctx, b, rg, "private-dns-records", *zone.Name, dns.NewRecordSetsClient().NewListPager(rg, *zone.Name, nil), func(p armprivatedns.RecordSetsClientListResponse) []*armprivatedns.RecordSet { return p.Value })
							})
							start(func() {
								collectAzurePages(ctx, b, rg, "private-dns-vnet-links", *zone.Name, dns.NewVirtualNetworkLinksClient().NewListPager(rg, *zone.Name, nil), func(p armprivatedns.VirtualNetworkLinksClientListResponse) []*armprivatedns.VirtualNetworkLink {
									return p.Value
								})
							})
						}
					}
					return p.Value
				})
			})
		} else {
			b.operation(ctx, rg, "private-dns", "", func(op *azureFailureOperation) error { op.Status = "skipped-client-unavailable"; return nil })
		}
		if compute != nil {
			start(func() { b.collectVMs(ctx, compute, rg) })
		} else {
			b.operation(ctx, rg, "virtual-machines", "", func(op *azureFailureOperation) error { op.Status = "skipped-client-unavailable"; return nil })
		}
	}
	branches.Wait()
	b.manifest.FinishedAt = time.Now().UTC()
	if err := b.saveManifest(); err != nil {
		b.errors = append(b.errors, fmt.Errorf("write manifest: %s", azureFailureError(err)))
	}
	return errors.Join(b.errors...)
}

func (b *azureFailureBundle) collectEffectiveNetwork(ctx context.Context, network *armnetwork.ClientFactory, rg, nic string) {
	client := network.NewInterfacesClient()
	var children sync.WaitGroup
	children.Go(func() {
		b.operation(ctx, rg, "effective-routes", nic, func(op *azureFailureOperation) error {
			poller, err := client.BeginGetEffectiveRouteTable(ctx, rg, nic, nil)
			if err != nil {
				return err
			}
			result, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Second})
			if err != nil {
				return err
			}
			return collectEffectivePages(ctx, b, op, result.Value, result.NextLink)
		})
	})
	children.Go(func() {
		b.operation(ctx, rg, "effective-network-security-groups", nic, func(op *azureFailureOperation) error {
			poller, err := client.BeginListEffectiveNetworkSecurityGroups(ctx, rg, nic, nil)
			if err != nil {
				return err
			}
			result, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Second})
			if err != nil {
				return err
			}
			return collectEffectivePages(ctx, b, op, result.Value, result.NextLink)
		})
	})
	children.Wait()
}

var errEffectiveNetworkIncomplete = errors.New("effective networking continuation unavailable or outside the ARM endpoint/resource scope")

// The network SDK exposes NextLink on effective-state LRO results but no pager.
// Use the same ARM options for continuation GETs, retaining each typed page.
func collectEffectivePages[T any](ctx context.Context, b *azureFailureBundle, op *azureFailureOperation, items []*T, next *string) error {
	for page := 1; ; page++ {
		if items == nil {
			items = []*T{}
		}
		op.Count += len(items)
		if err := b.writeJSON(op, fmt.Sprintf("page-%04d.json", page), struct {
			Value []*T `json:"value"`
		}{items}); err != nil {
			return err
		}
		if ptr.Deref(next, "") == "" {
			break
		}
		// Never send credentials to a continuation outside this ARM endpoint or
		// the NIC's resource group. Do not persist continuation URLs.
		link, err := url.Parse(*next)
		if err != nil || b.effectiveClient == nil {
			op.Status = "incomplete"
			return errEffectiveNetworkIncomplete
		}
		endpoint, err := url.Parse(b.effectiveClient.Endpoint())
		id, idErr := azcorearm.ParseResourceID(link.Path)
		if err != nil || link.Scheme != endpoint.Scheme || !strings.EqualFold(link.Host, endpoint.Host) || link.User != nil || idErr != nil || !strings.EqualFold(id.SubscriptionID, b.subscriptionID) || !strings.EqualFold(id.ResourceGroupName, op.ResourceGroup) {
			op.Status = "incomplete"
			return errEffectiveNetworkIncomplete
		}
		req, err := runtime.NewRequest(ctx, http.MethodGet, link.String())
		if err != nil {
			return err
		}
		resp, err := b.effectiveClient.Pipeline().Do(req)
		if err != nil {
			return err
		}
		if !runtime.HasStatusCode(resp, http.StatusOK) {
			return runtime.NewResponseError(resp)
		}
		var result struct {
			Value    []*T    `json:"value"`
			NextLink *string `json:"nextLink"`
		}
		if err := runtime.UnmarshalAsJSON(resp, &result); err != nil {
			return err
		}
		items, next = result.Value, result.NextLink
	}
	if op.Count == 0 {
		op.Status = "empty"
	}
	return nil
}

func (b *azureFailureBundle) collectVMs(ctx context.Context, compute *armcompute.ClientFactory, rg string) {
	var children sync.WaitGroup
	collectAzurePages(ctx, b, rg, "virtual-machines", "", compute.NewVirtualMachinesClient().NewListPager(rg, nil), func(p armcompute.VirtualMachinesClientListResponse) []*armcompute.VirtualMachine {
		var summaries []*armcompute.VirtualMachine
		for _, vm := range p.Value {
			if vm == nil {
				continue
			}
			// Construct an allowlist, rather than deleting sensitive fields from a VM.
			summary := &armcompute.VirtualMachine{ID: vm.ID, Name: vm.Name, Type: vm.Type, Location: vm.Location, Zones: vm.Zones, Identity: vm.Identity}
			if vm.Properties != nil {
				p := vm.Properties
				summary.Properties = &armcompute.VirtualMachineProperties{VMID: p.VMID, ProvisioningState: p.ProvisioningState, HardwareProfile: p.HardwareProfile, Priority: p.Priority, EvictionPolicy: p.EvictionPolicy}
				if p.NetworkProfile != nil {
					summary.Properties.NetworkProfile = &armcompute.NetworkProfile{NetworkInterfaces: p.NetworkProfile.NetworkInterfaces}
				}
				if p.StorageProfile != nil {
					summary.Properties.StorageProfile = &armcompute.StorageProfile{ImageReference: p.StorageProfile.ImageReference}
					if disk := p.StorageProfile.OSDisk; disk != nil {
						summary.Properties.StorageProfile.OSDisk = &armcompute.OSDisk{Name: disk.Name, OSType: disk.OSType, DiskSizeGB: disk.DiskSizeGB, Caching: disk.Caching, CreateOption: disk.CreateOption, DiffDiskSettings: disk.DiffDiskSettings}
					}
				}
			}
			summaries = append(summaries, summary)
			if ptr.Deref(vm.Name, "") != "" {
				children.Go(func() { b.collectVM(ctx, compute, rg, *vm.Name) })
			}
		}
		return summaries
	})
	children.Wait()
}

func (b *azureFailureBundle) collectVM(ctx context.Context, compute *armcompute.ClientFactory, rg, name string) {
	var children sync.WaitGroup
	children.Go(func() {
		// Instance view is independent of console availability, including success.
		b.operation(ctx, rg, "vm-instance-view", name, func(op *azureFailureOperation) error {
			result, err := compute.NewVirtualMachinesClient().InstanceView(ctx, rg, name, nil)
			if err != nil {
				return err
			}
			view := result.VirtualMachineInstanceView
			safe := armcompute.VirtualMachineInstanceView{
				Statuses: view.Statuses, ComputerName: view.ComputerName, OSName: view.OSName, OSVersion: view.OSVersion,
				HyperVGeneration: view.HyperVGeneration, PlatformFaultDomain: view.PlatformFaultDomain, PlatformUpdateDomain: view.PlatformUpdateDomain,
				MaintenanceRedeployStatus: view.MaintenanceRedeployStatus,
				AssignedHost:              view.AssignedHost, IsVMInStandbyPool: view.IsVMInStandbyPool, VMHealth: view.VMHealth,
			}
			if view.VMAgent != nil {
				safe.VMAgent = &armcompute.VirtualMachineAgentInstanceView{Statuses: view.VMAgent.Statuses, VMAgentVersion: view.VMAgent.VMAgentVersion}
			}
			for _, disk := range view.Disks {
				if disk != nil {
					safe.Disks = append(safe.Disks, &armcompute.DiskInstanceView{Name: disk.Name, Statuses: disk.Statuses})
				}
			}
			if view.BootDiagnostics != nil {
				safe.BootDiagnostics = &armcompute.BootDiagnosticsInstanceView{Status: view.BootDiagnostics.Status}
			}
			op.Count = 1
			return b.writeJSON(op, "instance-view.json", safe)
		})
	})
	children.Go(func() {
		b.operation(ctx, rg, "vm-console", name, func(op *azureFailureOperation) error {
			reader, err := GetVirtualMachineConsoleLog(ctx, compute, rg, name)
			if err != nil {
				return err
			}
			defer reader.Close()
			path := filepath.Join(azureArtifactName(rg), "vm-console", azureArtifactName(name)+"-console.log")
			if err := os.MkdirAll(filepath.Dir(filepath.Join(b.directory, path)), 0700); err != nil {
				return err
			}
			file, err := os.OpenFile(filepath.Join(b.directory, path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				return err
			}
			op.Artifacts = append(op.Artifacts, path)
			// Stream logs incrementally, excluding any credential-bearing URLs in
			// console output as well as those returned by the boot diagnostics API.
			lines := bufio.NewReader(reader)
			var copyErr error
			for {
				line, readErr := lines.ReadString('\n')
				if _, err := io.WriteString(file, redactAzureCredentialURLs(line)); err != nil {
					copyErr = err
					break
				}
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						copyErr = readErr
					}
					break
				}
			}
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
			op.Count = 1
			return nil
		})
	})
	children.Wait()
}
