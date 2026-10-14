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
// BackupRepository objects must remain for Kopia maintenance: neither a successful
// DeleteBackupRequest nor elapsed time proves that repository GC has completed.
package backupcleanup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	backupStore   cache.Store
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
	c := &Controller{
		name:          BackupCleanupControllerName,
		dynamicClient: dynamicClient,
		backupStore:   backupInformer.GetStore(),
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
				oldMeta, oldErr := meta.Accessor(old)
				currentMeta, currentErr := meta.Accessor(current)
				if oldErr == nil && currentErr == nil && oldMeta.GetResourceVersion() == currentMeta.GetResourceVersion() {
					return
				}
				c.enqueueBackup(current)
			},
			DeleteFunc: c.enqueueBackup,
		}},
		// Any HC preserves the namespace's backups, even while terminating.
		// Updates cannot change existence; only add/delete events matter.
		{hcInformer, cache.ResourceEventHandlerFuncs{
			AddFunc: c.enqueueHostedCluster, DeleteFunc: c.enqueueHostedCluster,
		}},
		{repoInformer, cache.ResourceEventHandlerFuncs{
			AddFunc: func(interface{}) { c.enqueueAll() },
			UpdateFunc: func(old, current interface{}) {
				oldRepo, oldOK := old.(*unstructured.Unstructured)
				currentRepo, currentOK := current.(*unstructured.Unstructured)
				if oldOK && currentOK && preserved(oldRepo) == preserved(currentRepo) &&
					field(oldRepo, "spec", "volumeNamespace") == field(currentRepo, "spec", "volumeNamespace") &&
					field(oldRepo, "spec", "backupStorageLocation") == field(currentRepo, "spec", "backupStorageLocation") {
					return
				}
				// Rescan both old and new associations, but not maintenance status.
				c.enqueueAll()
			},
			DeleteFunc: func(interface{}) { c.enqueueAll() },
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
	if err == nil && strings.HasPrefix(key, Namespace+"/") {
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
	for _, obj := range c.backupStore.List() {
		backup, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		hcNamespace, _, ok := backupNamespaces(backup)
		if ok && hcNamespace == namespace {
			c.enqueueBackup(backup)
		}
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

func (c *Controller) reconcile(ctx context.Context, key string) (bool, error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return false, err
	}
	if namespace != Namespace {
		return false, nil
	}
	backups := c.dynamicClient.Resource(BackupsGVR).Namespace(Namespace)
	backup, err := backups.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get backup: %w", err)
	}
	hcNamespace, cpNamespace, scoped := backupNamespaces(backup)
	if !scoped || preserved(backup) {
		return false, nil
	}
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddLogValuesForResourceIDString(backup.GetAnnotations()[controllerutils.HcpClusterAzureResourceIdAnnotation])...)
	ctx = utils.ContextWithLogger(ctx, logger)
	// Avoid scanning Velero resources for live clusters. HC deletion events
	// enqueue affected backups, so existence needs no periodic recheck.
	hcs, err := c.dynamicClient.Resource(HostedClustersGVR).Namespace(hcNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("check HostedCluster existence: %w", err)
	}
	if len(hcs.Items) != 0 {
		return false, nil
	}
	if hcs.GetContinue() != "" {
		return true, nil
	}
	if protected, err := c.repositoryPreserves(ctx, backup, hcNamespace, cpNamespace); err != nil || protected {
		// Repository events handle unprotection, without polling all operations.
		return false, err
	}
	if !terminalBackup(backup) {
		logger.V(4).Info("Preserving backup with nonterminal phase", "phase", field(backup, "status", "phase"))
		return true, nil
	}

	requests := c.dynamicClient.Resource(DeleteBackupRequestsGVR).Namespace(Namespace)
	// Velero adds backup labels asynchronously, so a selector would miss new
	// requests created by other actors and could allow duplicate requests.
	requestList, err := requests.List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("list deletion requests: %w", err)
	}
	if requestList.GetContinue() != "" {
		return false, fmt.Errorf("incomplete deletion request list")
	}
	requestName := deletionRequestName(backup)
	var processed *unstructured.Unstructured
	for i := range requestList.Items {
		request := &requestList.Items[i]
		if field(request, "spec", "backupName") != name && request.GetName() != requestName {
			continue
		}
		if request.GetName() != requestName || request.GetLabels()[backupUIDLabel] != string(backup.GetUID()) || field(request, "spec", "backupName") != name {
			return false, fmt.Errorf("another deletion request %q exists for backup; waiting for Velero", request.GetName())
		}
		if field(request, "status", "phase") != "Processed" || request.GetDeletionTimestamp() != nil {
			return true, nil
		}
		processed = request
	}

	// A terminal Backup can still have asynchronous uploads or an active restore.
	// Unknown/missing phases are active, not evidence that data is safe to remove.
	restoreNames, restoreUIDs := map[string]bool{}, map[string]bool{}
	for _, gvr := range []schema.GroupVersionResource{DataUploadsGVR, RestoresGVR, DataDownloadsGVR} {
		options := metav1.ListOptions{}
		if gvr == DataUploadsGVR {
			// Velero's CSI backup action sets this label when creating uploads.
			options.LabelSelector = labels.Set{backupNameLabel: veleroBackupName(name)}.String()
		}
		objects, err := c.dynamicClient.Resource(gvr).Namespace(Namespace).List(ctx, options)
		if err != nil {
			return false, fmt.Errorf("list %s: %w", gvr.Resource, err)
		}
		if objects.GetContinue() != "" {
			return false, fmt.Errorf("incomplete %s list", gvr.Resource)
		}
		for i := range objects.Items {
			obj := &objects.Items[i]
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
			logger.V(4).Info("Preserving backup with active operation", "resource", gvr.Resource, "name", obj.GetName())
			return true, nil
		}
	}

	// Recheck the current object, all repository opt-outs and HC absence before
	// each mutation. Never authorize a deletion using an informer cache miss.
	current, err := backups.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("recheck backup: %w", err)
	}
	if current.GetUID() != backup.GetUID() || current.GetResourceVersion() != backup.GetResourceVersion() || preserved(current) {
		return true, nil
	}
	currentHC, currentCP, scoped := backupNamespaces(current)
	if !scoped || !terminalBackup(current) || currentHC != hcNamespace || currentCP != cpNamespace || field(current, "spec", "storageLocation") != field(backup, "spec", "storageLocation") {
		return true, nil
	}
	if protected, err := c.repositoryPreserves(ctx, current, hcNamespace, cpNamespace); err != nil || protected {
		return false, err
	}
	hcs, err = c.dynamicClient.Resource(HostedClustersGVR).Namespace(hcNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Even NotFound (e.g. an unavailable CRD) is not proof of HC absence.
		return false, fmt.Errorf("confirm HostedCluster absence: %w", err)
	}
	if len(hcs.Items) != 0 {
		return false, nil
	}
	if hcs.GetContinue() != "" {
		return true, nil
	}
	if processed != nil {
		uid := processed.GetUID()
		if uid == "" {
			return false, fmt.Errorf("processed deletion request %q has no UID", processed.GetName())
		}
		logger.Info("Retrying processed deletion request while backup still exists", "request", processed.GetName(), "errors", processed.Object["status"])
		// A new request retries Velero's processed failures. UID/RV preconditions
		// prevent deleting a replacement request or one whose status changed.
		rv := processed.GetResourceVersion()
		err := requests.Delete(ctx, processed.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("remove processed deletion request: %w", err)
		}
		return true, nil
	}

	// Velero v1.18 records these labels but resolves spec.backupName without
	// checking the UID. They are NOT a server-side identity fence. The live GET
	// above narrows, but cannot eliminate, concurrent Backup name reuse or a new
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
	if _, err := requests.Create(ctx, request, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return true, nil
		}
		return false, fmt.Errorf("create deletion request: %w", err)
	}
	logger.Info("Requested Velero cleanup of orphaned backup", "request", requestName, "backupUID", backup.GetUID())
	return true, nil
}

func (c *Controller) repositoryPreserves(ctx context.Context, backup *unstructured.Unstructured, hcNamespace, cpNamespace string) (bool, error) {
	repositories, err := c.dynamicClient.Resource(BackupRepositoriesGVR).Namespace(Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, fmt.Errorf("check repositories: %w", err)
	}
	if repositories.GetContinue() != "" {
		return false, fmt.Errorf("incomplete repository list")
	}
	for i := range repositories.Items {
		repo := &repositories.Items[i]
		volumeNamespace := field(repo, "spec", "volumeNamespace")
		if preserved(repo) && (volumeNamespace == hcNamespace || volumeNamespace == cpNamespace) && field(repo, "spec", "backupStorageLocation") == field(backup, "spec", "storageLocation") {
			utils.LoggerFromContext(ctx).V(4).Info("Preserving backup associated with opted-out repository", "repository", repo.GetName())
			return true, nil
		}
	}
	return false, nil
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
