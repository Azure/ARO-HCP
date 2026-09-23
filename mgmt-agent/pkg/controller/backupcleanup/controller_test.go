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

package backupcleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic/dynamicinformer"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testHCNamespace = "ocm-arohcpint-2sd1pej7kdkk1qvccoiqq534ae1knhh4"
	testCPNamespace = testHCNamespace + "-my-cluster"
	testARMID       = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	return utils.ContextWithLogger(t.Context(), logr.Discard())
}

func object(kind, name string) *unstructured.Unstructured {
	version := "velero.io/v1"
	if kind == "HostedCluster" {
		version = "hypershift.openshift.io/v1beta1"
	}
	if kind == "DataUpload" || kind == "DataDownload" {
		version = "velero.io/v2alpha1"
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": version, "kind": kind,
		"metadata": map[string]interface{}{"name": name, "namespace": Namespace, "uid": name + "-uid", "resourceVersion": "1"},
	}}
}

func set(obj *unstructured.Unstructured, value interface{}, fields ...string) {
	if err := unstructured.SetNestedField(obj.Object, value, fields...); err != nil {
		panic(err)
	}
}

func testBackup() *unstructured.Unstructured {
	b := object("Backup", "backup")
	b.SetAnnotations(map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: testARMID})
	set(b, []interface{}{testHCNamespace, testCPNamespace}, "spec", "includedNamespaces")
	set(b, "bsl", "spec", "storageLocation")
	set(b, "Completed", "status", "phase")
	return b
}

func testRepo(namespace, bsl string, preserve bool) *unstructured.Unstructured {
	r := object("BackupRepository", "repo")
	set(r, namespace, "spec", "volumeNamespace")
	set(r, bsl, "spec", "backupStorageLocation")
	if preserve {
		r.SetAnnotations(map[string]string{PreserveBackupAnnotation: ""})
	}
	return r
}

func testHC() *unstructured.Unstructured {
	hc := object("HostedCluster", "my.cluster")
	hc.SetNamespace(testHCNamespace)
	return hc
}

func testRequest(backup *unstructured.Unstructured, phase string) *unstructured.Unstructured {
	r := object("DeleteBackupRequest", deletionRequestName(backup))
	r.SetLabels(map[string]string{backupUIDLabel: string(backup.GetUID()), backupNameLabel: veleroBackupName(backup.GetName())})
	set(r, backup.GetName(), "spec", "backupName")
	set(r, phase, "status", "phase")
	return r
}

func newTestController(t *testing.T, objects ...runtime.Object) (*Controller, *dynamicfake.FakeDynamicClient, dynamicinformer.DynamicSharedInformerFactory) {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		BackupsGVR: "BackupList", BackupRepositoriesGVR: "BackupRepositoryList", DeleteBackupRequestsGVR: "DeleteBackupRequestList",
		HostedClustersGVR: "HostedClusterList", DataUploadsGVR: "DataUploadList", RestoresGVR: "RestoreList", DataDownloadsGVR: "DataDownloadList",
	}, objects...)
	factory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	c, err := NewController(client, factory.ForResource(HostedClustersGVR).Informer(), factory.ForResource(BackupsGVR).Informer(), factory.ForResource(BackupRepositoriesGVR).Informer())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.queue.ShutDown)
	return c, client, factory
}

func writes(client *dynamicfake.FakeDynamicClient) []clienttesting.Action {
	var result []clienttesting.Action
	for _, action := range client.Actions() {
		switch action.GetVerb() {
		case "get", "list", "watch":
		default:
			result = append(result, action)
		}
	}
	return result
}

func assertNoWrites(t *testing.T, client *dynamicfake.FakeDynamicClient) {
	t.Helper()
	if actions := writes(client); len(actions) != 0 {
		t.Fatalf("unexpected mutations: %#v", actions)
	}
}

func assertRequest(t *testing.T, client *dynamicfake.FakeDynamicClient, backup *unstructured.Unstructured) {
	t.Helper()
	actions := writes(client)
	if len(actions) != 1 || actions[0].GetVerb() != "create" || actions[0].GetResource() != DeleteBackupRequestsGVR {
		t.Fatalf("expected only a DeleteBackupRequest create, got %#v", actions)
	}
	r := actions[0].(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured)
	if r.GetName() != deletionRequestName(backup) || r.GetNamespace() != Namespace || field(r, "spec", "backupName") != backup.GetName() || r.GetLabels()[backupUIDLabel] != string(backup.GetUID()) || r.GetLabels()[backupNameLabel] != veleroBackupName(backup.GetName()) {
		t.Fatalf("incorrect request: %#v", r.Object)
	}
	if len(r.GetOwnerReferences()) != 0 || len(validation.IsDNS1123Label(r.GetName())) != 0 {
		t.Fatalf("request must have a DNS-safe name and no owner references: %#v", r.Object)
	}
}

func TestBackupNamespaces(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*unstructured.Unstructured)
		valid  bool
	}{
		{"builder shape", func(*unstructured.Unstructured) {}, true},
		{"reversed", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testCPNamespace, testHCNamespace}, "spec", "includedNamespaces")
		}, true},
		{"missing annotation", func(b *unstructured.Unstructured) { b.SetAnnotations(nil) }, false},
		{"empty annotation", func(b *unstructured.Unstructured) {
			b.SetAnnotations(map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: ""})
		}, false},
		{"wrong resource type", func(b *unstructured.Unstructured) {
			b.SetAnnotations(map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: strings.ReplaceAll(testARMID, "hcpOpenShiftClusters", "openShiftClusters")})
		}, false},
		{"nodepool ARM ID", func(b *unstructured.Unstructured) {
			b.SetAnnotations(map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: testARMID + "/nodePools/pool"})
		}, false},
		{"ARM case insensitive", func(b *unstructured.Unstructured) {
			b.SetAnnotations(map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: strings.ToUpper(testARMID)})
		}, true},
		{"non ARM", func(b *unstructured.Unstructured) {
			b.SetAnnotations(map[string]string{controllerutils.HcpClusterAzureResourceIdAnnotation: "not-arm"})
		}, false},
		{"other namespace", func(b *unstructured.Unstructured) { b.SetNamespace("other") }, false},
		{"no UID", func(b *unstructured.Unstructured) { b.SetUID("") }, false},
		{"invalid UID label", func(b *unstructured.Unstructured) { b.SetUID("bad/uid") }, false},
		{"no BSL", func(b *unstructured.Unstructured) {
			unstructured.RemoveNestedField(b.Object, "spec", "storageLocation")
		}, false},
		{"missing namespaces", func(b *unstructured.Unstructured) {
			unstructured.RemoveNestedField(b.Object, "spec", "includedNamespaces")
		}, false},
		{"malformed namespaces", func(b *unstructured.Unstructured) { set(b, "*", "spec", "includedNamespaces") }, false},
		{"non string namespace", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, int64(1)}, "spec", "includedNamespaces")
		}, false},
		{"extra namespace", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, testCPNamespace, "extra"}, "spec", "includedNamespaces")
		}, false},
		{"only CP", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testCPNamespace}, "spec", "includedNamespaces")
		}, false},
		{"duplicate HC", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, testHCNamespace}, "spec", "includedNamespaces")
		}, false},
		{"wildcard", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, "*"}, "spec", "includedNamespaces")
		}, false},
		{"unrelated CP", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, "other-my-cluster"}, "spec", "includedNamespaces")
		}, false},
		{"empty suffix", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, testHCNamespace + "-"}, "spec", "includedNamespaces")
		}, false},
		{"un-normalized dots", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, testHCNamespace + "-my.cluster"}, "spec", "includedNamespaces")
		}, false},
		{"overlong CP", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, testHCNamespace + "-" + strings.Repeat("a", 30)}, "spec", "includedNamespaces")
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backup := testBackup()
			tt.mutate(backup)
			hc, cp, valid := backupNamespaces(backup)
			if valid != tt.valid || valid && (hc != testHCNamespace || cp != testCPNamespace) {
				t.Fatalf("got (%q, %q, %v), expected valid=%v", hc, cp, valid, tt.valid)
			}
			if !valid {
				c, client, _ := newTestController(t, backup)
				if _, err := c.reconcile(testContext(t), backup.GetNamespace()+"/"+backup.GetName()); err != nil {
					t.Fatal(err)
				}
				assertNoWrites(t, client)
			}
		})
	}
	for _, env := range []string{"a", "arohcpint", "arohcpstg", "arohcpprod", "arohcpci00", "arohcpci01", "arohcppers", "arohcpcspr", "abcdefghij", "0bad", "has-dash", "has.dot", "TooUpper", "abcdefghijk", ""} {
		t.Run("environment/"+env, func(t *testing.T) {
			b := testBackup()
			hc := "ocm-" + env + "-" + strings.Repeat("a", 32)
			set(b, []interface{}{hc, hc + "-hc-name"}, "spec", "includedNamespaces")
			_, _, valid := backupNamespaces(b)
			want := env != "0bad" && env != "has-dash" && env != "has.dot" && env != "TooUpper" && env != "abcdefghijk" && env != ""
			if valid != want {
				t.Fatalf("valid=%v, want %v", valid, want)
			}
		})
	}
	for _, id := range []string{"short", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("A", 32), strings.Repeat("a", 15) + "-" + strings.Repeat("b", 16)} {
		b := testBackup()
		hc := "ocm-arohcpint-" + id
		set(b, []interface{}{hc, hc + "-hc"}, "spec", "includedNamespaces")
		if _, _, valid := backupNamespaces(b); valid {
			t.Errorf("accepted invalid CS ID %q", id)
		}
	}
}

func TestReconcileOrphan(t *testing.T) {
	b := testBackup()
	r := testRepo(testCPNamespace, "bsl", false)
	c, client, _ := newTestController(t, b, r)
	recheck, err := c.reconcile(testContext(t), "velero/backup")
	if err != nil || !recheck {
		t.Fatalf("recheck=%v err=%v", recheck, err)
	}
	assertRequest(t, client, b)
	var hcLists, backupGets, repoLists int
	for _, action := range client.Actions() {
		if action.GetVerb() == "list" && action.GetResource() == HostedClustersGVR {
			hcLists++
			if action.GetNamespace() != testHCNamespace || !action.(clienttesting.ListAction).GetListRestrictions().Labels.Empty() {
				t.Fatalf("HC list must cover entire exact namespace: %#v", action)
			}
		}
		if action.GetVerb() == "get" && action.GetResource() == BackupsGVR {
			backupGets++
		}
		if action.GetVerb() == "list" && action.GetResource() == BackupRepositoriesGVR {
			repoLists++
		}
	}
	if hcLists != 2 || backupGets != 2 || repoLists != 2 {
		t.Fatalf("missing live safety reads: HC=%d Backup=%d Repo=%d", hcLists, backupGets, repoLists)
	}
	client.ClearActions()
	if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
		t.Fatal(err)
	}
	assertNoWrites(t, client)
}

func TestReconcileHostedClusterProtection(t *testing.T) {
	for _, name := range []string{"my.cluster", "my-cluster", "unrelated-name"} {
		for _, deleting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deleting=%v", name, deleting), func(t *testing.T) {
				hc := testHC()
				hc.SetName(name)
				if deleting {
					now := metav1.Now()
					hc.SetDeletionTimestamp(&now)
				}
				c, client, _ := newTestController(t, testBackup(), hc)
				if recheck, err := c.reconcile(testContext(t), "velero/backup"); err != nil || recheck {
					t.Fatalf("live HC should wait for deletion events, recheck=%v err=%v", recheck, err)
				}
				assertNoWrites(t, client)
				actions := client.Actions()
				if len(actions) != 2 || actions[0].GetVerb() != "get" || actions[0].GetResource() != BackupsGVR || actions[1].GetVerb() != "list" || actions[1].GetResource() != HostedClustersGVR {
					t.Fatalf("live HC must short-circuit all Velero lists: %#v", actions)
				}
				if err := client.Tracker().Delete(HostedClustersGVR, hc.GetNamespace(), hc.GetName()); err != nil {
					t.Fatal(err)
				}
				if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
					t.Fatal(err)
				}
				assertRequest(t, client, testBackup())
			})
		}
	}
	t.Run("HC in other namespace does not block", func(t *testing.T) {
		hc := testHC()
		hc.SetNamespace(testHCNamespace + "-other")
		c, client, _ := newTestController(t, testBackup(), hc)
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertRequest(t, client, testBackup())
	})
}

func TestBackupPhases(t *testing.T) {
	for _, phase := range []string{"", "New", "Queued", "ReadyToStart", "InProgress", "WaitingForPluginOperations", "WaitingForPluginOperationsPartiallyFailed", "Finalizing", "FinalizingPartiallyFailed", "Unknown", "Completed", "PartiallyFailed", "Failed", "FailedValidation", "Deleting"} {
		t.Run(phase, func(t *testing.T) {
			b := testBackup()
			set(b, phase, "status", "phase")
			c, client, _ := newTestController(t, b)
			recheck, err := c.reconcile(testContext(t), "velero/backup")
			if err != nil || !recheck {
				t.Fatalf("recheck=%v err=%v", recheck, err)
			}
			if phase == "Completed" || phase == "PartiallyFailed" || phase == "Failed" || phase == "FailedValidation" || phase == "Deleting" {
				assertRequest(t, client, b)
			} else {
				assertNoWrites(t, client)
			}
		})
	}
}

func TestOptOut(t *testing.T) {
	for _, value := range []string{"", "false", "true", "anything"} {
		t.Run("backup/"+value, func(t *testing.T) {
			b := testBackup()
			annotations := b.GetAnnotations()
			annotations[PreserveBackupAnnotation] = value
			b.SetAnnotations(annotations)
			c, client, _ := newTestController(t, b, testRequest(b, "Processed"))
			if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
				t.Fatal(err)
			}
			assertNoWrites(t, client)
		})
	}
	for _, tt := range []struct {
		name, namespace, bsl string
		preserve, protect    bool
	}{
		{"CP opted out", testCPNamespace, "bsl", true, true},
		{"HC opted out", testHCNamespace, "bsl", true, true},
		{"other BSL", testCPNamespace, "other", true, false},
		{"missing BSL", testCPNamespace, "", true, false},
		{"other namespace", "unrelated", "bsl", true, false},
		{"prefix only", testCPNamespace + "-other", "bsl", true, false},
		{"no annotation", testCPNamespace, "bsl", false, false},
		{"missing namespace", "", "bsl", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := testRepo(tt.namespace, tt.bsl, tt.preserve)
			other := testRepo(testCPNamespace, "bsl", false)
			other.SetName("first-repo")
			c, client, _ := newTestController(t, testBackup(), other, r)
			recheck, err := c.reconcile(testContext(t), "velero/backup")
			if err != nil {
				t.Fatal(err)
			}
			if tt.protect {
				assertNoWrites(t, client)
				if recheck {
					t.Fatal("repository opt-out must wait for events, not poll")
				}
				for _, action := range client.Actions() {
					if action.GetResource() != BackupsGVR && action.GetResource() != HostedClustersGVR && action.GetResource() != BackupRepositoriesGVR {
						t.Fatalf("preserved backup scanned operations or requests: %#v", action)
					}
				}
			} else {
				assertRequest(t, client, testBackup())
			}
		})
	}
	t.Run("all backups sharing opted out repository are protected", func(t *testing.T) {
		b1, b2 := testBackup(), testBackup()
		b2.SetName("backup-two")
		b2.SetUID("uid-two")
		c, client, _ := newTestController(t, b1, b2, testRepo(testCPNamespace, "bsl", true))
		for _, b := range []*unstructured.Unstructured{b1, b2} {
			if _, err := c.reconcile(testContext(t), Namespace+"/"+b.GetName()); err != nil {
				t.Fatal(err)
			}
		}
		assertNoWrites(t, client)
	})
}

func TestActiveOperations(t *testing.T) {
	for _, kind := range []string{"DataUpload", "Restore"} {
		for _, phase := range []string{"", "New", "Accepted", "Prepared", "InProgress", "Canceling", "WaitingForPluginOperations", "WaitingForPluginOperationsPartiallyFailed", "Finalizing", "FinalizingPartiallyFailed", "Unknown", "Deleting", "Completed", "PartiallyFailed", "Failed", "FailedValidation", "Canceled"} {
			t.Run(kind+"/"+phase, func(t *testing.T) {
				op := object(kind, "operation")
				set(op, phase, "status", "phase")
				if kind == "DataUpload" {
					op.SetLabels(map[string]string{backupNameLabel: "backup"})
				} else {
					set(op, "backup", "spec", "backupName")
				}
				c, client, _ := newTestController(t, testBackup(), op)
				if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
					t.Fatal(err)
				}
				terminal := phase == "Completed" || phase == "Failed" || kind == "DataUpload" && phase == "Canceled" || kind == "Restore" && (phase == "PartiallyFailed" || phase == "FailedValidation")
				if terminal {
					assertRequest(t, client, testBackup())
				} else {
					assertNoWrites(t, client)
				}
			})
		}
	}
	for _, match := range []string{"name", "both", "unrelated", "other namespace"} {
		t.Run("upload association/"+match, func(t *testing.T) {
			b := testBackup()
			b.SetName(strings.Repeat("long-", 16))
			op := object("DataUpload", "du")
			labels := map[string]string{}
			if match == "both" {
				labels[backupUIDLabel] = string(b.GetUID())
			}
			if match == "name" || match == "both" || match == "other namespace" {
				labels[backupNameLabel] = veleroBackupName(b.GetName())
			}
			op.SetLabels(labels)
			if match == "other namespace" {
				op.SetNamespace("elsewhere")
			}
			c, client, _ := newTestController(t, b, op)
			if _, err := c.reconcile(testContext(t), Namespace+"/"+b.GetName()); err != nil {
				t.Fatal(err)
			}
			for _, action := range client.Actions() {
				if action.GetResource() == DataUploadsGVR && action.GetVerb() == "list" {
					selector := action.(clienttesting.ListAction).GetListRestrictions().Labels.String()
					if selector != backupNameLabel+"="+veleroBackupName(b.GetName()) {
						t.Fatalf("uploads must be scoped by Velero backup name label: %q", selector)
					}
				}
			}
			if match == "unrelated" || match == "other namespace" {
				assertRequest(t, client, b)
			} else {
				assertNoWrites(t, client)
			}
		})
	}
	t.Run("unrelated restore", func(t *testing.T) {
		r := object("Restore", "restore")
		set(r, "other", "spec", "backupName")
		c, client, _ := newTestController(t, testBackup(), r)
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertRequest(t, client, testBackup())
	})
}

func TestScheduleRestores(t *testing.T) {
	for _, tt := range []struct {
		name, schedule, backupName, phase string
		protect                           bool
	}{
		{"unresolved new", "daily", "", "New", true},
		{"unresolved no phase", "daily", "", "", true},
		{"unresolved active", "daily", "", "InProgress", true},
		{"unrelated schedule", "other", "", "New", false},
		{"no schedule", "", "", "New", false},
		{"resolved different backup", "daily", "other", "InProgress", false},
		{"resolved this backup", "daily", "backup", "InProgress", true},
		{"terminal unresolved", "daily", "", "Failed", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := testBackup()
			b.SetLabels(map[string]string{scheduleNameLabel: "daily"})
			r := object("Restore", "restore")
			set(r, tt.schedule, "spec", "scheduleName")
			set(r, tt.backupName, "spec", "backupName")
			set(r, tt.phase, "status", "phase")
			c, client, _ := newTestController(t, b, r)
			if recheck, err := c.reconcile(testContext(t), "velero/backup"); err != nil || !recheck {
				t.Fatalf("recheck=%v err=%v", recheck, err)
			}
			if tt.protect {
				assertNoWrites(t, client)
			} else {
				assertRequest(t, client, b)
			}
		})
	}
}

func TestDataDownloads(t *testing.T) {
	t.Run("failed unresolved schedule restore", func(t *testing.T) {
		b := testBackup()
		b.SetLabels(map[string]string{scheduleNameLabel: "daily"})
		r := object("Restore", "restore")
		set(r, "daily", "spec", "scheduleName")
		set(r, "Failed", "status", "phase")
		dd := object("DataDownload", "download")
		dd.SetLabels(map[string]string{restoreUIDLabel: string(r.GetUID())})
		c, client, _ := newTestController(t, b, r, dd)
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, client)
	})
	for _, phase := range []string{"", "New", "Accepted", "Prepared", "InProgress", "Canceling", "Unknown", "Completed", "Failed", "Canceled"} {
		t.Run("failed restore/"+phase, func(t *testing.T) {
			r := object("Restore", "restore")
			set(r, "backup", "spec", "backupName")
			set(r, "Failed", "status", "phase")
			dd := object("DataDownload", "download")
			dd.SetLabels(map[string]string{restoreNameLabel: r.GetName(), restoreUIDLabel: string(r.GetUID())})
			set(dd, phase, "status", "phase")
			set(dd, true, "spec", "cancel") // A cancellation request is not completion.
			c, client, _ := newTestController(t, testBackup(), r, dd)
			if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
				t.Fatal(err)
			}
			if phase == "Completed" || phase == "Failed" || phase == "Canceled" {
				assertRequest(t, client, testBackup())
			} else {
				assertNoWrites(t, client)
			}
		})
	}
	for _, tt := range []struct {
		name, source, bsl, label string
		missingRestore, protect  bool
	}{
		{"restore UID only", "", "", restoreUIDLabel, false, true},
		{"restore name only", "", "", restoreNameLabel, false, true},
		{"unrelated labels", "", "", "unrelated", false, false},
		{"missing restore CP fallback", testCPNamespace, "bsl", "", true, true},
		{"missing restore HC fallback", testHCNamespace, "bsl", "", true, true},
		{"missing restore other BSL", testCPNamespace, "other", "", true, false},
		{"missing restore other source", "other", "bsl", "", true, false},
		{"missing restore no association", "", "", restoreUIDLabel, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Long names must use Velero's truncated+hashed restore-name label.
			r := object("Restore", strings.Repeat("restore-", 10))
			r.SetUID("restore-uid")
			set(r, "backup", "spec", "backupName")
			set(r, "Completed", "status", "phase")
			dd := object("DataDownload", "download")
			if tt.label == restoreUIDLabel {
				dd.SetLabels(map[string]string{restoreUIDLabel: string(r.GetUID())})
			}
			if tt.label == restoreNameLabel {
				dd.SetLabels(map[string]string{restoreNameLabel: veleroBackupName(r.GetName())})
			}
			if tt.label == "unrelated" {
				dd.SetLabels(map[string]string{restoreNameLabel: "other", restoreUIDLabel: "other"})
			}
			set(dd, tt.source, "spec", "sourceNamespace")
			set(dd, tt.bsl, "spec", "backupStorageLocation")
			set(dd, "remapped-target", "spec", "targetVolume", "namespace")
			objects := []runtime.Object{testBackup(), dd}
			if !tt.missingRestore {
				objects = append(objects, r)
			}
			c, client, _ := newTestController(t, objects...)
			if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
				t.Fatal(err)
			}
			if tt.protect {
				assertNoWrites(t, client)
			} else {
				assertRequest(t, client, testBackup())
			}
		})
	}
}

func TestLiveReadFailures(t *testing.T) {
	for _, gvr := range []schema.GroupVersionResource{DeleteBackupRequestsGVR, DataUploadsGVR, RestoresGVR, DataDownloadsGVR, BackupRepositoriesGVR} {
		t.Run("incomplete/"+gvr.Resource, func(t *testing.T) {
			c, client, _ := newTestController(t, testBackup())
			client.PrependReactor("list", gvr.Resource, func(clienttesting.Action) (bool, runtime.Object, error) {
				list := &unstructured.UnstructuredList{}
				list.SetContinue("more")
				return true, list, nil
			})
			if _, err := c.reconcile(testContext(t), "velero/backup"); err == nil {
				t.Fatal("incomplete safety list must fail closed")
			}
			assertNoWrites(t, client)
		})
	}
	for _, tt := range []struct {
		verb string
		gvr  schema.GroupVersionResource
		nth  int
	}{
		{"get", BackupsGVR, 1}, {"get", BackupsGVR, 2}, {"list", BackupRepositoriesGVR, 1}, {"list", BackupRepositoriesGVR, 2}, {"list", DataDownloadsGVR, 1},
		{"list", HostedClustersGVR, 1}, {"list", HostedClustersGVR, 2}, {"list", DeleteBackupRequestsGVR, 1}, {"list", DataUploadsGVR, 1}, {"list", RestoresGVR, 1},
	} {
		for _, failure := range []error{errors.New("unavailable"), apierrors.NewForbidden(tt.gvr.GroupResource(), "", errors.New("denied")), apierrors.NewNotFound(tt.gvr.GroupResource(), "missing")} {
			t.Run(fmt.Sprintf("%s/%s/%d/%v", tt.verb, tt.gvr.Resource, tt.nth, failure), func(t *testing.T) {
				c, client, _ := newTestController(t, testBackup())
				calls := 0
				client.PrependReactor(tt.verb, tt.gvr.Resource, func(clienttesting.Action) (bool, runtime.Object, error) {
					calls++
					if calls == tt.nth {
						return true, nil, failure
					}
					return false, nil, nil
				})
				_, err := c.reconcile(testContext(t), "velero/backup")
				if (tt.gvr != BackupsGVR || !apierrors.IsNotFound(failure)) && err == nil {
					t.Fatal("expected read error")
				}
				assertNoWrites(t, client)
			})
		}
	}
	t.Run("incomplete HC list", func(t *testing.T) {
		c, client, _ := newTestController(t, testBackup())
		client.PrependReactor("list", "hostedclusters", func(clienttesting.Action) (bool, runtime.Object, error) {
			list := &unstructured.UnstructuredList{}
			list.SetContinue("more")
			return true, list, nil
		})
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, client)
	})
}

func TestLiveRechecks(t *testing.T) {
	t.Run("repository opt-out appears before mutation", func(t *testing.T) {
		c, client, _ := newTestController(t, testBackup())
		lists := 0
		client.PrependReactor("list", "backuprepositories", func(clienttesting.Action) (bool, runtime.Object, error) {
			lists++
			if lists == 2 {
				return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*testRepo(testCPNamespace, "bsl", true)}}, nil
			}
			return false, nil, nil
		})
		if recheck, err := c.reconcile(testContext(t), "velero/backup"); err != nil || recheck {
			t.Fatalf("late repository opt-out must block and wait for events, recheck=%v err=%v", recheck, err)
		}
		if lists != 2 {
			t.Fatalf("expected final repository recheck, got %d", lists)
		}
		assertNoWrites(t, client)
	})
	t.Run("HC appears after early absence check", func(t *testing.T) {
		c, client, _ := newTestController(t, testBackup())
		lists := 0
		client.PrependReactor("list", "hostedclusters", func(clienttesting.Action) (bool, runtime.Object, error) {
			lists++
			if lists == 2 {
				return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*testHC()}}, nil
			}
			return false, nil, nil
		})
		if recheck, err := c.reconcile(testContext(t), "velero/backup"); err != nil || recheck {
			t.Fatalf("new HC must block mutation and polling, recheck=%v err=%v", recheck, err)
		}
		if lists != 2 {
			t.Fatalf("expected final live HC recheck, got %d lists", lists)
		}
		assertNoWrites(t, client)
	})
	for _, tt := range []struct {
		name   string
		mutate func(*unstructured.Unstructured)
	}{
		{"recreated UID", func(b *unstructured.Unstructured) { b.SetUID("new-uid") }},
		{"resource version changed", func(b *unstructured.Unstructured) { b.SetResourceVersion("2") }},
		{"opt out appeared", func(b *unstructured.Unstructured) {
			a := b.GetAnnotations()
			a[PreserveBackupAnnotation] = ""
			b.SetAnnotations(a)
		}},
		{"active phase", func(b *unstructured.Unstructured) { set(b, "InProgress", "status", "phase") }},
		{"BSL changed", func(b *unstructured.Unstructured) { set(b, "new-bsl", "spec", "storageLocation") }},
		{"namespace changed", func(b *unstructured.Unstructured) {
			set(b, []interface{}{testHCNamespace, testHCNamespace + "-other"}, "spec", "includedNamespaces")
		}},
		{"scope lost", func(b *unstructured.Unstructured) { b.SetAnnotations(nil) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, client, _ := newTestController(t, testBackup())
			gets := 0
			client.PrependReactor("get", "backups", func(clienttesting.Action) (bool, runtime.Object, error) {
				gets++
				if gets != 2 {
					return false, nil, nil
				}
				b := testBackup()
				tt.mutate(b)
				return true, b, nil
			})
			if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
				t.Fatal(err)
			}
			assertNoWrites(t, client)
		})
	}
	t.Run("live repository opt out not present in cache", func(t *testing.T) {
		c, client, _ := newTestController(t, testBackup(), testRepo(testCPNamespace, "bsl", true))
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, client)
	})
	t.Run("stale backup cache cannot authorize current object", func(t *testing.T) {
		b := testBackup()
		a := b.GetAnnotations()
		a[PreserveBackupAnnotation] = ""
		b.SetAnnotations(a)
		c, client, _ := newTestController(t, b)
		if err := c.backupStore.Add(testBackup()); err != nil {
			t.Fatal(err)
		}
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertNoWrites(t, client)
	})
}

func TestDeletionRequests(t *testing.T) {
	t.Run("unlabeled external pending request", func(t *testing.T) {
		b := testBackup()
		r := testRequest(b, "New")
		r.SetName("external-request")
		r.SetLabels(nil)
		c, client, _ := newTestController(t, b, r)
		if _, err := c.reconcile(testContext(t), "velero/backup"); err == nil {
			t.Fatal("external request must block and retry, even before Velero labels it")
		}
		assertNoWrites(t, client)
		for _, action := range client.Actions() {
			if action.GetResource() == DeleteBackupRequestsGVR && action.GetVerb() == "list" && !action.(clienttesting.ListAction).GetListRestrictions().Labels.Empty() {
				t.Fatal("request list selector would miss unlabeled requests")
			}
		}
	})
	for _, phase := range []string{"", "New", "InProgress", "Unknown"} {
		t.Run("pending/"+phase, func(t *testing.T) {
			b := testBackup()
			c, client, _ := newTestController(t, b, testRequest(b, phase))
			recheck, err := c.reconcile(testContext(t), "velero/backup")
			if err != nil || !recheck {
				t.Fatalf("recheck=%v err=%v", recheck, err)
			}
			assertNoWrites(t, client)
		})
	}
	for _, withErrors := range []bool{false, true} {
		t.Run(fmt.Sprintf("processed/errors=%v", withErrors), func(t *testing.T) {
			b := testBackup()
			set(b, "Deleting", "status", "phase")
			r := testRequest(b, "Processed")
			if withErrors {
				set(r, []interface{}{"storage unavailable"}, "status", "errors")
			}
			c, client, _ := newTestController(t, b, r)
			if again, err := c.reconcile(testContext(t), "velero/backup"); err != nil || !again {
				t.Fatalf("recheck=%v err=%v", again, err)
			}
			actions := writes(client)
			if len(actions) != 1 || actions[0].GetVerb() != "delete" || actions[0].GetResource() != DeleteBackupRequestsGVR {
				t.Fatalf("expected only processed request delete, got %#v", actions)
			}
			preconditions := actions[0].(clienttesting.DeleteAction).GetDeleteOptions().Preconditions
			if preconditions == nil || preconditions.UID == nil || *preconditions.UID != r.GetUID() || preconditions.ResourceVersion == nil || *preconditions.ResourceVersion != r.GetResourceVersion() {
				t.Fatalf("missing request identity preconditions: %#v", preconditions)
			}
			client.ClearActions()
			if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
				t.Fatal(err)
			}
			assertRequest(t, client, b)
		})
	}
	for _, tt := range []struct {
		name      string
		mutate    func(*unstructured.Unstructured)
		wantError bool
	}{
		{"external request", func(r *unstructured.Unstructured) { r.SetName("external") }, true},
		{"wrong UID", func(r *unstructured.Unstructured) { r.SetLabels(map[string]string{backupUIDLabel: "other"}) }, true},
		{"missing UID label", func(r *unstructured.Unstructured) { r.SetLabels(nil) }, true},
		{"name collision", func(r *unstructured.Unstructured) { set(r, "other", "spec", "backupName") }, true},
		{"missing request UID", func(r *unstructured.Unstructured) { r.SetUID("") }, true},
		{"terminating request", func(r *unstructured.Unstructured) { now := metav1.Now(); r.SetDeletionTimestamp(&now) }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := testBackup()
			r := testRequest(b, "Processed")
			tt.mutate(r)
			c, client, _ := newTestController(t, b, r)
			_, err := c.reconcile(testContext(t), "velero/backup")
			if (err != nil) != tt.wantError {
				t.Fatalf("err=%v, wantError=%v", err, tt.wantError)
			}
			assertNoWrites(t, client)
		})
	}
	t.Run("unrelated request", func(t *testing.T) {
		b := testBackup()
		other := testBackup()
		other.SetName("other")
		other.SetUID("other-uid")
		c, client, _ := newTestController(t, b, testRequest(other, "New"))
		if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
			t.Fatal(err)
		}
		assertRequest(t, client, b)
	})
	for _, protection := range []string{"HC", "repository", "UID"} {
		t.Run("processed protected/"+protection, func(t *testing.T) {
			b := testBackup()
			r := testRequest(b, "Processed")
			objects := []runtime.Object{b, r}
			if protection == "HC" {
				objects = append(objects, testHC())
			}
			if protection == "repository" {
				objects = append(objects, testRepo(testCPNamespace, "bsl", true))
			}
			c, client, _ := newTestController(t, objects...)
			if protection == "UID" {
				gets := 0
				client.PrependReactor("get", "backups", func(clienttesting.Action) (bool, runtime.Object, error) {
					gets++
					if gets == 2 {
						replacement := b.DeepCopy()
						replacement.SetUID("new-uid")
						return true, replacement, nil
					}
					return false, nil, nil
				})
			}
			if _, err := c.reconcile(testContext(t), "velero/backup"); err != nil {
				t.Fatal(err)
			}
			assertNoWrites(t, client)
		})
	}
}

func TestWriteFailures(t *testing.T) {
	for _, verb := range []string{"create", "delete"} {
		for _, failure := range []error{errors.New("unavailable"), apierrors.NewConflict(DeleteBackupRequestsGVR.GroupResource(), "request", errors.New("changed")), apierrors.NewAlreadyExists(DeleteBackupRequestsGVR.GroupResource(), "request"), apierrors.NewNotFound(DeleteBackupRequestsGVR.GroupResource(), "request")} {
			t.Run(verb+"/"+failure.Error(), func(t *testing.T) {
				b := testBackup()
				objects := []runtime.Object{b}
				if verb == "delete" {
					objects = append(objects, testRequest(b, "Processed"))
				}
				c, client, _ := newTestController(t, objects...)
				client.PrependReactor(verb, "deletebackuprequests", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, failure })
				_, err := c.reconcile(testContext(t), "velero/backup")
				handled := verb == "create" && apierrors.IsAlreadyExists(failure) || verb == "delete" && apierrors.IsNotFound(failure)
				if (err == nil) != handled {
					t.Fatalf("err=%v handled=%v", err, handled)
				}
				if len(writes(client)) != 1 {
					t.Fatal("must not attempt additional mutations after failure")
				}
			})
		}
	}
}

func TestNames(t *testing.T) {
	b := testBackup()
	first := deletionRequestName(b)
	b.SetName("renamed")
	if deletionRequestName(b) != first {
		t.Fatal("request identity must depend on UID, not name")
	}
	b.SetUID(types.UID("new-uid"))
	if deletionRequestName(b) == first {
		t.Fatal("new UID must get a new request name")
	}
	for _, name := range []string{"backup", strings.Repeat("a", 63), strings.Repeat("a", 64), strings.Repeat("a", 253)} {
		label := veleroBackupName(name)
		if len(label) > 63 || len(validation.IsValidLabelValue(label)) != 0 {
			t.Fatalf("invalid label %q", label)
		}
		if len(name) <= 63 && label != name {
			t.Fatal("short name must not change")
		}
	}
	// sha256 of 64 'a' characters begins ffe054 (Velero label.GetValidName).
	if got := veleroBackupName(strings.Repeat("a", 64)); got != strings.Repeat("a", 57)+"ffe054" {
		t.Fatalf("Velero long-name label mismatch: %q", got)
	}
}

func TestEnqueue(t *testing.T) {
	c, _, _ := newTestController(t)
	b := testBackup()
	other := testBackup()
	other.SetName("other")
	set(other, []interface{}{"ocm-a-" + strings.Repeat("b", 32), "ocm-a-" + strings.Repeat("b", 32) + "-hc"}, "spec", "includedNamespaces")
	for _, obj := range []interface{}{b, other, &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "typed", Namespace: "elsewhere"}}} {
		if err := c.backupStore.Add(obj); err != nil {
			t.Fatal(err)
		}
	}
	for _, hc := range []interface{}{testHC(), &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "my.cluster", Namespace: testHCNamespace}}, cache.DeletedFinalStateUnknown{Key: testHCNamespace + "/my.cluster", Obj: testHC()}} {
		c.enqueueHostedCluster(hc)
		if c.queue.Len() != 1 {
			t.Fatalf("expected only affected backup, queued %d", c.queue.Len())
		}
		key, _ := c.queue.Get()
		c.queue.Done(key)
		c.queue.Forget(key)
		if key != "velero/backup" {
			t.Fatalf("unexpected key %q", key)
		}
	}
	c.enqueueHostedCluster("invalid")
	c.enqueueHostedCluster(&unstructured.Unstructured{})
	c.enqueueBackup("invalid")
	if c.queue.Len() != 0 {
		t.Fatal("invalid events must not enqueue")
	}
	c.enqueueBackup(cache.DeletedFinalStateUnknown{Key: "velero/backup", Obj: b})
	c.enqueueAll()
	if c.queue.Len() != 2 {
		t.Fatalf("expected all velero backups, got %d", c.queue.Len())
	}
}

// Capture the actual registered callbacks without asynchronous watch timing.
type handlerInformer struct {
	cache.SharedIndexInformer
	handler cache.ResourceEventHandler
}

func (i *handlerInformer) AddEventHandler(handler cache.ResourceEventHandler) (cache.ResourceEventHandlerRegistration, error) {
	i.handler = handler
	return i.SharedIndexInformer.AddEventHandler(handler)
}

func TestInformerUpdateFilters(t *testing.T) {
	_, client, factory := newTestController(t)
	hc := &handlerInformer{SharedIndexInformer: factory.ForResource(HostedClustersGVR).Informer()}
	backup := &handlerInformer{SharedIndexInformer: factory.ForResource(BackupsGVR).Informer()}
	repo := &handlerInformer{SharedIndexInformer: factory.ForResource(BackupRepositoriesGVR).Informer()}
	c, err := NewController(client, hc, backup, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.queue.ShutDown()
	if err := c.backupStore.Add(testBackup()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		informer *handlerInformer
		old      *unstructured.Unstructured
		mutate   func(*unstructured.Unstructured)
		want     bool
	}{
		{"repo status", repo, testRepo(testCPNamespace, "bsl", true), func(o *unstructured.Unstructured) { set(o, "Ready", "status", "phase") }, false},
		{"repo resource version", repo, testRepo(testCPNamespace, "bsl", true), func(*unstructured.Unstructured) {}, false},
		{"repo preserve value only", repo, testRepo(testCPNamespace, "bsl", true), func(o *unstructured.Unstructured) {
			o.SetAnnotations(map[string]string{PreserveBackupAnnotation: "false"})
		}, false},
		{"repo preserve added", repo, testRepo(testCPNamespace, "bsl", false), func(o *unstructured.Unstructured) { o.SetAnnotations(map[string]string{PreserveBackupAnnotation: ""}) }, true},
		{"repo preserve removed", repo, testRepo(testCPNamespace, "bsl", true), func(o *unstructured.Unstructured) { o.SetAnnotations(nil) }, true},
		{"repo namespace", repo, testRepo(testCPNamespace, "bsl", true), func(o *unstructured.Unstructured) { set(o, "other", "spec", "volumeNamespace") }, true},
		{"repo BSL", repo, testRepo(testCPNamespace, "bsl", true), func(o *unstructured.Unstructured) { set(o, "other", "spec", "backupStorageLocation") }, true},
		{"HC status", hc, testHC(), func(o *unstructured.Unstructured) { set(o, "Ready", "status", "phase") }, false},
		{"HC terminating", hc, testHC(), func(o *unstructured.Unstructured) { now := metav1.Now(); o.SetDeletionTimestamp(&now) }, false},
		{"backup resync", backup, testBackup(), func(o *unstructured.Unstructured) { o.SetResourceVersion("1") }, false},
		{"backup changed", backup, testBackup(), func(o *unstructured.Unstructured) { set(o, "Failed", "status", "phase") }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			current := tt.old.DeepCopy()
			current.SetResourceVersion("2")
			tt.mutate(current)
			tt.informer.handler.OnUpdate(tt.old, current)
			if got := c.queue.Len() != 0; got != tt.want {
				t.Fatalf("enqueued=%v, want %v", got, tt.want)
			}
			if c.queue.Len() != 0 {
				key, _ := c.queue.Get()
				c.queue.Done(key)
			}
		})
	}
	for _, tt := range []struct {
		informer *handlerInformer
		obj      interface{}
	}{{hc, testHC()}, {backup, testBackup()}, {repo, testRepo(testCPNamespace, "bsl", true)}} {
		key, err := cache.MetaNamespaceKeyFunc(tt.obj)
		if err != nil {
			t.Fatal(err)
		}
		for _, notify := range []func(){func() { tt.informer.handler.OnAdd(tt.obj, true) }, func() { tt.informer.handler.OnDelete(cache.DeletedFinalStateUnknown{Key: key, Obj: tt.obj}) }} {
			notify()
			if c.queue.Len() != 1 {
				t.Fatal("add/delete must enqueue affected backup")
			}
			key, _ := c.queue.Get()
			c.queue.Done(key)
		}
	}
	assertNoWrites(t, client)
}

func TestRunAndInformerEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	b := testBackup()
	r := testRepo(testCPNamespace, "bsl", true)
	c, client, factory := newTestController(t, b, r, testHC())
	factory.Start(ctx.Done())
	for gvr, synced := range factory.WaitForCacheSync(ctx.Done()) {
		if !synced {
			t.Fatalf("cache %s not synced", gvr)
		}
	}
	// Start with genuinely synced indexes, and validate startup add events.
	if _, exists, err := c.backupStore.GetByKey("velero/backup"); err != nil || !exists {
		t.Fatalf("backup cache missing: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, 2) }()
	waitFor(t, func() bool {
		for _, action := range client.Actions() {
			if action.GetVerb() == "get" && action.GetResource() == BackupsGVR {
				return true
			}
		}
		return false
	})
	assertNoWrites(t, client)
	if err := client.Resource(HostedClustersGVR).Namespace(testHCNamespace).Delete(ctx, "my.cluster", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	// The repository still protects the backup after HC deletion. Removing the
	// annotation produces a repository update event, which must enqueue it.
	r.SetAnnotations(nil)
	if _, err := client.Resource(BackupRepositoriesGVR).Namespace(Namespace).Update(ctx, r, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		_, err := client.Tracker().Get(DeleteBackupRequestsGVR, Namespace, deletionRequestName(b))
		return err == nil
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	for _, action := range writes(client) {
		if action.GetResource() == BackupsGVR || action.GetResource() == BackupRepositoriesGVR && action.GetVerb() != "update" {
			t.Fatalf("controller directly mutated backup/repository: %#v", action)
		}
	}
}

func TestQueueRetries(t *testing.T) {
	for _, scenario := range []string{"pending request", "active upload", "error"} {
		t.Run(scenario, func(t *testing.T) {
			b := testBackup()
			objects := []runtime.Object{b}
			if scenario == "pending request" {
				objects = append(objects, testRequest(b, "InProgress"))
			}
			if scenario == "active upload" {
				du := object("DataUpload", "du")
				du.SetLabels(map[string]string{backupNameLabel: "backup"})
				objects = append(objects, du)
			}
			c, client, _ := newTestController(t, objects...)
			c.queue.ShutDown()
			clock := clocktesting.NewFakeClock(time.Now())
			c.queue = workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{Clock: clock})
			t.Cleanup(c.queue.ShutDown)
			if scenario == "error" {
				client.PrependReactor("get", "backups", func(clienttesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("unavailable") })
			}
			c.queue.Add("velero/backup")
			if !c.processNext(testContext(t)) {
				t.Fatal("worker stopped")
			}
			if scenario == "error" && c.queue.NumRequeues("velero/backup") != 1 {
				t.Fatal("error not rate limited")
			}
			if scenario != "error" && c.queue.NumRequeues("velero/backup") != 0 {
				t.Fatal("successful wait should forget retry count")
			}
			waitFor(t, func() bool { clock.Step(recheckInterval); return c.queue.Len() != 0 })
			assertNoWrites(t, client)
			c.queue.ShutDown()
		})
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := NewController(nil, nil, nil, nil); err == nil {
		t.Fatal("nil inputs accepted")
	}
	t.Run("zero workers", func(t *testing.T) {
		c, _, _ := newTestController(t)
		if err := c.Run(testContext(t), 0); err == nil {
			t.Fatal("zero workers accepted")
		}
	})
	t.Run("unsynced caches", func(t *testing.T) {
		c, client, _ := newTestController(t, testBackup())
		ctx, cancel := context.WithCancel(testContext(t))
		cancel()
		if err := c.Run(ctx, 1); err == nil {
			t.Fatal("unsynced caches accepted")
		}
		assertNoWrites(t, client)
	})
	t.Run("key and missing backup", func(t *testing.T) {
		c, client, _ := newTestController(t)
		for _, key := range []string{"other/backup", "backup", "velero/missing"} {
			if _, err := c.reconcile(testContext(t), key); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.reconcile(testContext(t), "too/many/parts"); err == nil {
			t.Fatal("invalid key accepted")
		}
		assertNoWrites(t, client)
		c.queue.ShutDown()
		if c.processNext(testContext(t)) {
			t.Fatal("shutdown queue kept worker running")
		}
	})
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not satisfied before timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
