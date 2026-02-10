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

package recovery

import (
	"context"
	"fmt"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
)

type DrClient struct {
	client ctrlClient.Client
}

// AddToScheme registers the types needed by DrClient.
func AddToScheme(scheme *runtime.Scheme) error {
	if err := velerov1api.AddToScheme(scheme); err != nil {
		return err
	}
	return hypershiftv1beta1.AddToScheme(scheme)
}

// NewDrClient creates a DrClient wrapping an already-configured controller-runtime client.
func NewDrClient(client ctrlClient.Client) *DrClient {
	return &DrClient{client: client}
}

func (c *DrClient) GetBackup(ctx context.Context, backupName string) (*velerov1api.Backup, error) {
	backup := &velerov1api.Backup{}
	key := ctrlClient.ObjectKey{Name: backupName, Namespace: "velero"}
	if err := c.client.Get(ctx, key, backup); err != nil {
		return nil, err
	}
	return backup, nil
}

func (c *DrClient) ListBackupsForCluster(ctx context.Context, clusterId string) ([]velerov1api.Backup, error) {
	backupList := &velerov1api.BackupList{}
	if err := c.client.List(ctx, backupList, ctrlClient.MatchingLabels{"api.openshift.com/id": clusterId}); err != nil {
		return nil, err
	}
	return backupList.Items, nil
}

func (c *DrClient) CreateBackupForCluster(ctx context.Context, clusterId string) (*velerov1api.Backup, error) {
	hc, err := c.GetHostedCluster(ctx, clusterId)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		return nil, fmt.Errorf("hosted cluster %s not found", clusterId)
	}

	now := time.Now().UTC().Format("2006-01-02-150405")
	backupName := fmt.Sprintf("%s-%s", clusterId, now)
	hcpNamespace := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)

	backup := NewBackup(backupName, clusterId, hc.Namespace, hcpNamespace)
	err = c.client.Create(ctx, backup)
	if err != nil {
		return nil, err
	}

	return backup, nil
}

func (c *DrClient) GetHostedCluster(ctx context.Context, clusterId string) (*hypershiftv1beta1.HostedCluster, error) {
	hostedClusters := &hypershiftv1beta1.HostedClusterList{}
	if err := c.client.List(ctx, hostedClusters, ctrlClient.MatchingLabels{"api.openshift.com/id": clusterId}); err != nil {
		return nil, err
	}
	if len(hostedClusters.Items) == 0 {
		return nil, nil
	} else if len(hostedClusters.Items) > 1 {
		return nil, fmt.Errorf("multiple hosted clusters found for cluster %s", clusterId)
	}
	hc := &hostedClusters.Items[0]
	return hc, nil
}
