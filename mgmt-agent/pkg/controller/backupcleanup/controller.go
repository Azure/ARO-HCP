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

// Package backupcleanup requests Velero-managed cleanup of orphaned ARO backups.
// Repository retirement is handled separately after the backups are gone.
package backupcleanup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	BackupCleanupControllerName = "BackupCleanup"
	Namespace                   = "velero"
	PreserveBackupAnnotation    = "mgmt-agent.aro-hcp.azure.com/preserve-backup"
	backupUIDLabel              = "velero.io/backup-uid"
	backupNameLabel             = "velero.io/backup-name"
	restoreNameLabel            = "velero.io/restore-name"
	restoreUIDLabel             = "velero.io/restore-uid"
	scheduleNameLabel           = "velero.io/schedule-name"
	includedNamespaceIndex      = "backupcleanup/includedNamespace"
	recheckInterval             = time.Minute
)

var (
	BackupsGVR              = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backups"}
	BackupRepositoriesGVR   = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backuprepositories"}
	DeleteBackupRequestsGVR = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "deletebackuprequests"}
	HostedClustersGVR       = schema.GroupVersionResource{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters"}
	DataUploadsGVR          = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datauploads"}
	DataDownloadsGVR        = schema.GroupVersionResource{Group: "velero.io", Version: "v2alpha1", Resource: "datadownloads"}
	RestoresGVR             = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "restores"}

	// Cluster Service's namespace contract, including config's 1-10 character
	// environment identifier. Do not split a control-plane namespace to guess
	// a HostedCluster name: names may contain both dots and hyphens.
	hostedClusterNamespace = regexp.MustCompile(`^ocm-[a-z][a-z0-9]{0,9}-[a-z0-9]{32}$`)
)

type Controller struct {
	name          string
	dynamicClient dynamic.Interface
	backupStore   cache.Indexer
	hasSynced     []cache.InformerSynced
	queue         workqueue.TypedRateLimitingInterface[string]
}

// NewController accepts either a typed or an unstructured HostedCluster informer.
// The caller starts the informers; Backup and BackupRepository informers must
// include all objects in Namespace, without label filtering.
func NewController(dynamicClient dynamic.Interface, hcInformer cache.SharedIndexInformer, backupInformer cache.SharedIndexInformer, repoInformer cache.SharedIndexInformer) (*Controller, error) {
	if dynamicClient == nil || hcInformer == nil || backupInformer == nil || repoInformer == nil {
		return nil, fmt.Errorf("client and all three informers are required")
	}
	if err := backupInformer.AddIndexers(cache.Indexers{includedNamespaceIndex: func(obj interface{}) ([]string, error) {
		backup, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return nil, nil
		}
		// Routing deliberately ignores eligibility, annotations and phase.
		namespaces, _, _ := unstructured.NestedStringSlice(backup.Object, "spec", "includedNamespaces")
		return namespaces, nil
	}}); err != nil {
		return nil, fmt.Errorf("index backup namespaces: %w", err)
	}
	c := &Controller{
		name:          BackupCleanupControllerName,
		dynamicClient: dynamicClient,
		backupStore:   backupInformer.GetIndexer(),
		hasSynced:     []cache.InformerSynced{hcInformer.HasSynced, backupInformer.HasSynced, repoInformer.HasSynced},
		queue:         workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[string](), workqueue.TypedRateLimitingQueueConfig[string]{Name: BackupCleanupControllerName}),
	}
	for _, registration := range []struct {
		informer cache.SharedIndexInformer
		handler  cache.ResourceEventHandlerFuncs
	}{
		{backupInformer, cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueueBackup,
			UpdateFunc: func(old, current interface{}) {
				c.enqueueBackup(old)
				c.enqueueBackup(current)
			},
			DeleteFunc: c.enqueueBackup,
		}},
		{hcInformer, cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueueHostedCluster, DeleteFunc: c.enqueueHostedCluster,
			UpdateFunc: func(old, current interface{}) {
				c.enqueueHostedCluster(old)
				c.enqueueHostedCluster(current)
			},
		}},
		{repoInformer, cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueueRepository,
			UpdateFunc: func(old, current interface{}) {
				c.enqueueRepository(old)
				c.enqueueRepository(current)
			},
			DeleteFunc: c.enqueueRepository,
		}},
	} {
		if _, err := registration.informer.AddEventHandler(registration.handler); err != nil {
			c.queue.ShutDown()
			return nil, fmt.Errorf("register informer handler: %w", err)
		}
	}
	return c, nil
}

func (c *Controller) enqueueBackup(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err == nil {
		c.queue.Add(key)
	}
}

func (c *Controller) enqueueAll() {
	for _, obj := range c.backupStore.List() {
		c.enqueueBackup(obj)
	}
}

func (c *Controller) enqueueHostedCluster(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return
	}
	namespace, _, err := cache.SplitMetaNamespaceKey(key)
	if err != nil || namespace == "" {
		return
	}
	c.enqueueNamespace(namespace)
}

func (c *Controller) enqueueRepository(obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	if repo, ok := obj.(*unstructured.Unstructured); ok {
		c.enqueueNamespace(field(repo, "spec", "volumeNamespace"))
	}
}

func (c *Controller) enqueueNamespace(namespace string) {
	objects, err := c.backupStore.ByIndex(includedNamespaceIndex, namespace)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	for _, obj := range objects {
		c.enqueueBackup(obj)
	}
}

// Run waits for caches and runs workers until cancellation. Informer state is
// only an event source; only uncached API reads authorize deletion requests.
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()
	if workers < 1 {
		return fmt.Errorf("workers must be positive")
	}
	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	if !cache.WaitForCacheSync(ctx.Done(), c.hasSynced...) {
		return fmt.Errorf("failed to sync backup cleanup informer caches")
	}
	c.enqueueAll()
	logger.Info("Starting backup cleanup controller", "workers", workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			for c.processNext(ctx) {
			}
		}()
	}
	<-ctx.Done()
	c.queue.ShutDown()
	wg.Wait()
	logger.Info("Stopped backup cleanup controller")
	return nil
}

func (c *Controller) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	if namespace, name, err := cache.SplitMetaNamespaceKey(key); err == nil {
		logger = logger.WithValues("namespace", namespace, "backup", name)
	}
	ctx = utils.ContextWithLogger(ctx, logger)
	recheck, err := c.reconcile(ctx, key)
	if err != nil {
		logger.Error(err, "Backup cleanup failed; retrying")
		c.queue.AddRateLimited(key)
	} else {
		c.queue.Forget(key)
		if recheck {
			// Requests, transfers and Restores have no informer here. Poll
			// their progress, but never interpret elapsed time as GC success.
			c.queue.AddAfter(key, recheckInterval)
		}
	}
	return true
}

// Observations contain only live API results. A nil list is unobserved, not empty.
type observations struct {
	backup, previous *unstructured.Unstructured
	lists            map[schema.GroupVersionResource]*unstructured.UnstructuredList
}

type deletionTarget struct {
	name            string
	uid             types.UID
	resourceVersion string
}

type plan struct {
	observe schema.GroupVersionResource
	apply   *unstructured.Unstructured
	delete  *deletionTarget
	requeue bool
}

func (c *Controller) reconcile(ctx context.Context, key string) (bool, error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return false, err
	}
	if namespace != Namespace {
		return false, nil
	}
	var previous *unstructured.Unstructured
	var desired plan
	// Gather and plan twice before writing, including requests and operations on
	// the second pass. Each pass may stop early only when the pure plan is safe.
	for pass := 0; pass < 2; pass++ {
		backup, err := c.dynamicClient.Resource(BackupsGVR).Namespace(Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("get backup: %w", err)
		}
		if apierrors.IsNotFound(err) {
			backup = nil
		}
		observed := observations{backup: backup, previous: previous, lists: map[schema.GroupVersionResource]*unstructured.UnstructuredList{}}
		for {
			desired, err = planCleanup(observed)
			if err != nil {
				return false, err
			}
			if desired.observe.Empty() {
				break
			}
			gvr := desired.observe
			namespace := Namespace
			options := metav1.ListOptions{}
			if gvr == HostedClustersGVR {
				namespace, _, _ = backupNamespaces(backup)
			}
			if gvr == DataUploadsGVR {
				options.LabelSelector = labels.Set{backupNameLabel: veleroBackupName(name)}.String()
			}
			objects, err := c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, options)
			if err != nil {
				return false, fmt.Errorf("list %s: %w", gvr.Resource, err)
			}
			observed.lists[gvr] = objects
		}
		if desired.apply != nil {
			// Guard the deterministic SSA name with a live identity observation,
			// including requests that appeared after the unfiltered list.
			request, err := c.dynamicClient.Resource(DeleteBackupRequestsGVR).Namespace(Namespace).Get(ctx, desired.apply.GetName(), metav1.GetOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("get deletion request: %w", err)
			}
			if err == nil {
				observed.lists[DeleteBackupRequestsGVR].Items = append(observed.lists[DeleteBackupRequestsGVR].Items, *request)
				desired, err = planCleanup(observed)
				if err != nil {
					return false, err
				}
			}
		}
		if desired.apply == nil && desired.delete == nil {
			return desired.requeue, nil
		}
		previous = backup
	}
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddLogValuesForResourceIDString(previous.GetAnnotations()[controllerutils.HcpClusterAzureResourceIdAnnotation])...)
	ctx = utils.ContextWithLogger(ctx, logger)
	return desired.requeue, c.execute(ctx, desired)
}

// planCleanup is pure: missing observations request reads, never authorize writes.
func planCleanup(observed observations) (plan, error) {
	backup := observed.backup
	if backup == nil {
		return plan{}, nil
	}
	if observed.previous != nil && !reflect.DeepEqual(observed.previous.Object, backup.Object) {
		return plan{requeue: true}, nil
	}
	hcNamespace, cpNamespace, scoped := backupNamespaces(backup)
	if !scoped || preserved(backup) {
		return plan{}, nil
	}
	// Preserve short-circuiting for live clusters and opted-out repositories.
	for _, gvr := range []schema.GroupVersionResource{HostedClustersGVR, BackupRepositoriesGVR, DeleteBackupRequestsGVR} {
		objects := observed.lists[gvr]
		if objects == nil {
			return plan{observe: gvr}, nil
		}
		if objects.GetContinue() != "" {
			return plan{}, fmt.Errorf("incomplete %s list", gvr.Resource)
		}
		if gvr == HostedClustersGVR && len(objects.Items) != 0 {
			return plan{}, nil
		}
		if gvr == BackupRepositoriesGVR {
			for i := range objects.Items {
				repo := &objects.Items[i]
				volumeNamespace := field(repo, "spec", "volumeNamespace")
				if preserved(repo) && (volumeNamespace == hcNamespace || volumeNamespace == cpNamespace) && field(repo, "spec", "backupStorageLocation") == field(backup, "spec", "storageLocation") {
					return plan{}, nil
				}
			}
			if !terminalBackup(backup) {
				return plan{requeue: true}, nil
			}
		}
	}
	name := backup.GetName()
	// No selector: Velero labels requests asynchronously, including external ones.
	requestList := observed.lists[DeleteBackupRequestsGVR]
	requestName := deletionRequestName(backup)
	var processed *unstructured.Unstructured
	for i := range requestList.Items {
		request := &requestList.Items[i]
		if field(request, "spec", "backupName") != name && request.GetName() != requestName {
			continue
		}
		if request.GetName() != requestName || request.GetLabels()[backupUIDLabel] != string(backup.GetUID()) || field(request, "spec", "backupName") != name {
			return plan{}, fmt.Errorf("another deletion request %q exists for backup; waiting for Velero", request.GetName())
		}
		if field(request, "status", "phase") != "Processed" || request.GetDeletionTimestamp() != nil {
			return plan{requeue: true}, nil
		}
		processed = request
	}

	// A terminal Backup can still have asynchronous uploads or an active restore.
	// Unknown/missing phases are active, not evidence that data is safe to remove.
	restoreNames, restoreUIDs := map[string]bool{}, map[string]bool{}
	for _, gvr := range []schema.GroupVersionResource{DataUploadsGVR, RestoresGVR, DataDownloadsGVR} {
		objects := observed.lists[gvr]
		if objects == nil {
			return plan{observe: gvr}, nil
		}
		if objects.GetContinue() != "" {
			return plan{}, fmt.Errorf("incomplete %s list", gvr.Resource)
		}
		for i := range objects.Items {
			obj := &objects.Items[i]
			if gvr == DataUploadsGVR && obj.GetLabels()[backupNameLabel] != veleroBackupName(name) {
				continue
			}
			if gvr == DataUploadsGVR || gvr == DataDownloadsGVR {
				switch field(obj, "status", "phase") {
				case "Completed", "Failed", "Canceled":
					continue
				}
				if gvr == DataDownloadsGVR {
					// A Failed Restore can still be asynchronously canceling downloads.
					// If the Restore disappeared, source namespace + BSL still identifies
					// a shared repository to protect (target namespaces may be remapped).
					sourceNamespace := field(obj, "spec", "sourceNamespace")
					sameRepository := (sourceNamespace == hcNamespace || sourceNamespace == cpNamespace) && field(obj, "spec", "backupStorageLocation") == field(backup, "spec", "storageLocation")
					if !restoreNames[obj.GetLabels()[restoreNameLabel]] && !restoreUIDs[obj.GetLabels()[restoreUIDLabel]] && !sameRepository {
						continue
					}
				}
			} else {
				restoreBackup := field(obj, "spec", "backupName")
				schedule := field(obj, "spec", "scheduleName")
				if restoreBackup != name && (restoreBackup != "" || schedule == "" || schedule != backup.GetLabels()[scheduleNameLabel]) {
					continue
				}
				restoreNames[veleroBackupName(obj.GetName())] = true
				if obj.GetUID() != "" {
					restoreUIDs[string(obj.GetUID())] = true
				}
				if terminalBackup(obj) {
					continue
				}
			}
			return plan{requeue: true}, nil
		}
	}
	if processed != nil {
		if processed.GetUID() == "" || processed.GetResourceVersion() == "" {
			return plan{}, fmt.Errorf("processed deletion request %q has no UID or resource version", processed.GetName())
		}
		// A new request retries Velero's processed failures. UID/RV preconditions
		// prevent deleting a replacement request or one whose status changed.
		return plan{delete: &deletionTarget{name: processed.GetName(), uid: processed.GetUID(), resourceVersion: processed.GetResourceVersion()}, requeue: true}, nil
	}

	// Velero v1.18 records these labels but resolves spec.backupName without
	// checking the UID. They are NOT a server-side identity fence. Live reads
	// narrow, but cannot eliminate, concurrent Backup name reuse or a new
	// HC/opt-out/operation after these checks. Opt-outs cannot cancel requests
	// already handed to Velero. Do not add a Backup ownerReference: Kubernetes GC
	// must not remove the request while Velero is cleaning external data.
	request := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "velero.io/v1", "kind": "DeleteBackupRequest",
		"metadata": map[string]interface{}{
			"name": requestName, "namespace": Namespace,
			"labels": map[string]interface{}{backupUIDLabel: string(backup.GetUID()), backupNameLabel: veleroBackupName(name)},
		},
		"spec": map[string]interface{}{"backupName": name},
	}}
	return plan{apply: request, requeue: true}, nil
}

func (c *Controller) execute(ctx context.Context, desired plan) error {
	requests := c.dynamicClient.Resource(DeleteBackupRequestsGVR).Namespace(Namespace)
	if desired.delete != nil {
		target := desired.delete
		if err := requests.Delete(ctx, target.name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &target.uid, ResourceVersion: &target.resourceVersion}}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("remove processed deletion request: %w", err)
		}
		utils.LoggerFromContext(ctx).Info("Retrying processed deletion request while backup still exists", "request", target.name)
	}
	if desired.apply != nil {
		// Only desired metadata/spec are applied; status belongs exclusively to Velero.
		if _, err := requests.Apply(ctx, desired.apply.GetName(), desired.apply, metav1.ApplyOptions{FieldManager: BackupCleanupControllerName, Force: false}); err != nil {
			return fmt.Errorf("apply deletion request: %w", err)
		}
		utils.LoggerFromContext(ctx).Info("Requested Velero cleanup of orphaned backup", "request", desired.apply.GetName(), "backupUID", desired.apply.GetLabels()[backupUIDLabel])
	}
	return nil
}

// backupNamespaces recognizes the ARO builder's exact two-namespace shape,
// independent of list order. Legacy orphan cleanup intentionally relies on this
// strict namespace contract; nonconforming backups need operator intervention.
func backupNamespaces(backup *unstructured.Unstructured) (string, string, bool) {
	if backup.GetNamespace() != Namespace || backup.GetUID() == "" || len(validation.IsValidLabelValue(string(backup.GetUID()))) != 0 || field(backup, "spec", "storageLocation") == "" {
		return "", "", false
	}
	id, err := azcorearm.ParseResourceID(backup.GetAnnotations()[controllerutils.HcpClusterAzureResourceIdAnnotation])
	if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" || !strings.EqualFold(id.ResourceType.String(), "Microsoft.RedHatOpenShift/hcpOpenShiftClusters") {
		return "", "", false
	}
	namespaces, _, err := unstructured.NestedStringSlice(backup.Object, "spec", "includedNamespaces")
	if err != nil || len(namespaces) != 2 {
		return "", "", false
	}
	for i, hcNamespace := range namespaces {
		cpNamespace := namespaces[1-i]
		if !hostedClusterNamespace.MatchString(hcNamespace) || !strings.HasPrefix(cpNamespace, hcNamespace+"-") || len(validation.IsDNS1123Label(cpNamespace)) != 0 {
			continue
		}
		// Forward construction is HC namespace + '-' + HC name with dots
		// replaced by hyphens. Never infer an HC name from the suffix.
		suffix := strings.TrimPrefix(cpNamespace, hcNamespace+"-")
		if len(validation.IsDNS1123Label(suffix)) == 0 {
			return hcNamespace, cpNamespace, true
		}
	}
	return "", "", false
}

func terminalBackup(obj *unstructured.Unstructured) bool {
	switch field(obj, "status", "phase") {
	case "Completed", "PartiallyFailed", "Failed", "FailedValidation":
		return true
	case "Deleting":
		// Velero leaves failed deletions in this phase. Permit retrying them,
		// but never treat a restore with an unknown phase as terminal.
		return obj.GetKind() == "Backup"
	default:
		return false
	}
}

func preserved(obj *unstructured.Unstructured) bool {
	_, ok := obj.GetAnnotations()[PreserveBackupAnnotation]
	return ok
}

func field(obj *unstructured.Unstructured, fields ...string) string {
	value, _, _ := unstructured.NestedString(obj.Object, fields...)
	return value
}

func deletionRequestName(backup *unstructured.Unstructured) string {
	return fmt.Sprintf("arohcp-%x", sha256.Sum224([]byte(backup.GetUID())))
}

// Match Velero's label.GetValidName without importing the Velero dependency.
func veleroBackupName(name string) string {
	if len(name) <= 63 {
		return name
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
	return name[:57] + hash[:6]
}
