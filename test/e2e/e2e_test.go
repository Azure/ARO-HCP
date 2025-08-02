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

//go:build E2Etests

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/log"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ARO-HCP E2E Tests")
}

var _ = BeforeSuite(func() {
	if err := setup(context.Background()); err != nil {
		panic(err)
	}
})

var _ = AfterSuite(func() {
	// Only attempt deletion of HCP cluster if a cluster was actually created
	if e2eSetup.Cluster.Name == "" || e2eSetup.CustomerEnv.CustomerRGName == "" {
		return
	}
	log.Logger.Infof("Starting deletion of cluster %s in resource group %s...", e2eSetup.Cluster.Name, e2eSetup.CustomerEnv.CustomerRGName)
	ctxDel, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	hcpClient := clients.NewHcpOpenShiftClustersClient()
	clusterName := e2eSetup.Cluster.Name
	resourceGroup := e2eSetup.CustomerEnv.CustomerRGName
	poller, err := hcpClient.BeginDelete(ctxDel, resourceGroup, clusterName, nil)
	if err != nil {
		panic(fmt.Sprintf("Cluster deletion should succeed (begin): %v", err))
	}
	_, err = poller.PollUntilDone(ctxDel, &runtime.PollUntilDoneOptions{
		Frequency: 10 * time.Second,
	})
	if err != nil {
		panic(fmt.Sprintf("Cluster deletion should succeed (poll): %v", err))
	}
})
