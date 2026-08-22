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

package backup

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	velerov1api "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
)

func TestNewBackup_EtcdSnapshotHook(t *testing.T) {
	b := NewBackup("test-backup", "/subscriptions/x/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c", "ns-hc", "ocm-cp", time.Hour)

	require.Len(t, b.Spec.Hooks.Resources, 1, "expected exactly one backup resource hook")
	hook := b.Spec.Hooks.Resources[0]

	assert.Equal(t, "etcd-snapshot", hook.Name)
	assert.Equal(t, []string{"pods"}, hook.IncludedResources)
	assert.Equal(t, []string{"ocm-cp"}, hook.IncludedNamespaces, "hook must be scoped to the control plane namespace")

	require.NotNil(t, hook.LabelSelector, "hook must have a label selector to target etcd members")
	assert.Equal(t, map[string]string{"app": "etcd"}, hook.LabelSelector.MatchLabels)

	require.Len(t, hook.PreHooks, 1, "expected exactly one pre-hook")
	exec := hook.PreHooks[0].Exec
	require.NotNil(t, exec, "pre-hook must be an exec hook")

	assert.Equal(t, "etcd", exec.Container)
	assert.Equal(t, velerov1api.HookErrorModeFail, exec.OnError)
	assert.Equal(t, 10*time.Minute, exec.Timeout.Duration)

	require.Len(t, exec.Command, 3, "exec command should be a shell invocation")
	assert.Equal(t, "/bin/sh", exec.Command[0])
	assert.Equal(t, "-c", exec.Command[1])
	cmd := exec.Command[2]
	assert.Contains(t, cmd, "ETCDCTL_API=3")
	assert.Contains(t, cmd, "snapshot save /var/lib/backup.db")
	// The rename done by `snapshot save` must be flushed to the block device before
	// the CSI snapshot, or the PVC restores with only backup.db.part (rename lost).
	assert.Contains(t, cmd, "&& sync -f /var/lib/backup.db", "hook must fsync the data PVC after the snapshot save to avoid the .part race")
	assert.Contains(t, cmd, "--cacert /etc/etcd/tls/etcd-ca/ca.crt")
	assert.Contains(t, cmd, "--cert /etc/etcd/tls/client/etcd-client.crt")
	assert.Contains(t, cmd, "--key /etc/etcd/tls/client/etcd-client.key")
	// Snapshot is pulled from the client Service (healthy member), not localhost or a pod.
	assert.Contains(t, cmd, "--endpoints=https://etcd-client.ocm-cp.svc:2379")

	// A post-hook cleans the snapshot artifact off the live PVC after the CSI
	// snapshot is cut, so backup.db does not linger during normal operation.
	require.Len(t, hook.PostHooks, 1, "expected exactly one post-hook to clean up backup.db")
	post := hook.PostHooks[0].Exec
	require.NotNil(t, post, "post-hook must be an exec hook")
	assert.Equal(t, "etcd", post.Container)
	// Cleanup must never fail an otherwise-good backup.
	assert.Equal(t, velerov1api.HookErrorModeContinue, post.OnError, "post-hook cleanup must not fail the backup")
	require.Len(t, post.Command, 3, "post-hook command should be a shell invocation")
	assert.Contains(t, post.Command[2], "rm -f /var/lib/backup.db", "post-hook must remove the snapshot artifact from the live PVC")
	assert.Contains(t, post.Command[2], "/var/lib/backup.db.part", "post-hook should also remove a stray .part from an interrupted save")
}

func TestNewBackup_VolumeSnapshotOptionsUnchanged(t *testing.T) {
	b := NewBackup("test-backup", "resource-id", "ns-hc", "ocm-cp", time.Hour)

	require.NotNil(t, b.Spec.SnapshotVolumes)
	assert.True(t, *b.Spec.SnapshotVolumes, "SnapshotVolumes must remain enabled")

	require.NotNil(t, b.Spec.SnapshotMoveData)
	assert.True(t, *b.Spec.SnapshotMoveData, "SnapshotMoveData must remain enabled")

	assert.Contains(t, b.Spec.IncludedResources, "pvc")
	assert.Contains(t, b.Spec.IncludedResources, "pv")
	// pods must be included or Velero never collects the etcd pods and the
	// pre-exec snapshot hook never runs (hookStatus stays empty).
	assert.Contains(t, b.Spec.IncludedResources, "pods")
}
