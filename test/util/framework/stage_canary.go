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
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// DO NOT MERGE: this temporary harness provisions short-lived Stage canaries.
const (
	StageCanarySpecPrefix          = "Stage canary should provision a retained control plane for OpenShift "
	StageCanarySpecCount           = 3
	StageCanaryResourceGroupPrefix = "stage-canary"
	StageCanarySubscription        = "ARO HCP E2E Hosted Clusters - Stage - 00"
	retainedCanaryJobIDTag         = "retainedJobID.aro-hcp-ci.redhat.com"
	stageCanaryRetention           = 7 * 24 * time.Hour
)

func StageCanaryEnabled() bool {
	return os.Getenv("ARO_HCP_CLOUD") == "public" &&
		os.Getenv("ARO_HCP_DEPLOY_ENV") == "stg" &&
		os.Getenv("ARO_HCP_SUITE_NAME") == "stage/parallel"
}

// RetainStageCanaryResourceGroup hands a successfully verified canary from
// immediate test/job cleanup to the existing periodic expiry cleaner. Failed
// provisioning attempts remain tracked and are cleaned up normally.
func (tc *perItOrDescribeTestContext) RetainStageCanaryResourceGroup(ctx context.Context, name string) (time.Time, error) {
	jobID := os.Getenv("BUILD_ID")
	if !StageCanaryEnabled() || os.Getenv("CUSTOMER_SUBSCRIPTION") != StageCanarySubscription || tc.UsePooledIdentities() || strings.TrimSpace(jobID) == "" || tc.perBinaryInvocationTestContext.sharedDir == "" {
		return time.Time{}, fmt.Errorf("retaining a canary requires the Stage Prow suite and expiry-covered subscription, dedicated identities, BUILD_ID and SHARED_DIR")
	}
	tc.contextLock.RLock()
	onlyCanaryGroup := len(tc.knownResourceGroups) == 1 && tc.knownResourceGroups[0] == name && len(tc.knownAppRegistrations) == 0
	tc.contextLock.RUnlock()
	if !strings.HasPrefix(name, StageCanaryResourceGroupPrefix+"-") || !onlyCanaryGroup {
		return time.Time{}, fmt.Errorf("can only retain this test's single canary resource group without app registrations")
	}
	clientFactory, err := tc.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return time.Time{}, err
	}
	client := clientFactory.NewResourceGroupsClient()
	group, err := client.Get(ctx, name, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading canary resource group: %w", err)
	}
	if ptr.Deref(group.Tags[ProwJobIDTag], "") != jobID || ptr.Deref(group.Tags["e2e.aro-hcp-ci.redhat.com"], "") != "true" {
		return time.Time{}, fmt.Errorf("canary resource group must belong to this Prow job and remain eligible for expiry cleanup")
	}

	// Preserve build attribution under an explicit retention tag rather than
	// the immediate-cleanup job tag. Keep the e2e tag and a bounded expiry so
	// the unchanged periodic expired-RG cleaner will still find this group.
	expires := time.Now().UTC().Add(stageCanaryRetention)
	tags := maps.Clone(group.Tags)
	tags[retainedCanaryJobIDTag] = ptr.To(jobID)
	tags["deleteAfter.aro-hcp-ci.redhat.com"] = ptr.To(expires.Format(time.RFC3339))
	delete(tags, ProwJobIDTag)
	_, err = client.Update(ctx, name, armresources.ResourceGroupPatchable{Tags: tags}, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("setting canary retention tags: %w", err)
	}
	saved, err := client.Get(ctx, name, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading back canary retention tags: %w", err)
	}
	if saved.Tags[ProwJobIDTag] != nil ||
		ptr.Deref(saved.Tags[retainedCanaryJobIDTag], "") != jobID ||
		ptr.Deref(saved.Tags["e2e.aro-hcp-ci.redhat.com"], "") != "true" ||
		ptr.Deref(saved.Tags["deleteAfter.aro-hcp-ci.redhat.com"], "") != expires.Format(time.RFC3339) {
		return time.Time{}, fmt.Errorf("canary retention tags did not persist for resource group %q", name)
	}
	// Post-job cleanup discovers groups through BOTH tracked files and BUILD_ID
	// tags. Unregister only this group, after its expiry/attribution are saved.
	marker := filepath.Join(tc.perBinaryInvocationTestContext.sharedDir, "tracked-resource-group_"+name)
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return time.Time{}, fmt.Errorf("unregistering retained canary from immediate cleanup: %w", err)
	}
	tc.retainedStageCanary = true
	return expires, nil
}
