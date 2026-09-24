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

package repositorycleanup

import (
	"crypto/sha3"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/Azure/ARO-HCP/mgmt-agent/pkg/controller/repositorycleanup/storage"
)

var volumePattern = regexp.MustCompile(`^(ocm-[a-z][a-z0-9]{0,9}-[a-z0-9]{32})(?:-([a-z0-9](?:[-a-z0-9]*[a-z0-9])?))?$`)

// HostedClusterNamespace derives only a namespace, never a HostedCluster name.
func HostedClusterNamespace(volume string) string {
	match := volumePattern.FindStringSubmatch(volume)
	if len(match) == 0 || len(validation.IsDNS1123Label(volume)) != 0 {
		return ""
	}
	return match[1]
}

// RepositoryLabel matches Velero label.ReturnNameOrHash, not GetValidName (the
// latter uses a different hash and is used for backup labels).
func RepositoryLabel(name string) string {
	if len(name) <= 63 {
		return name
	}
	return fmt.Sprintf("%x", sha3.Sum224([]byte(name)))
}

func field(obj *unstructured.Unstructured, path ...string) string {
	if obj == nil {
		return ""
	}
	value, _, _ := unstructured.NestedString(obj.Object, path...)
	return value
}

func preserved(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	_, ok := obj.GetAnnotations()[PreserveBackupAnnotation]
	return ok
}

func hasFinalizer(obj *unstructured.Unstructured) bool {
	return obj != nil && slices.Contains(obj.GetFinalizers(), Finalizer)
}

func identity(repo *unstructured.Unstructured) RepositoryIdentity {
	return RepositoryIdentity{Name: repo.GetName(), UID: repo.GetUID(), VolumeNamespace: field(repo, "spec", "volumeNamespace")}
}

func resolve(repo *unstructured.Unstructured, objects map[schema.GroupVersionResource][]unstructured.Unstructured) (storage.Target, error) {
	for _, bsl := range objects[BackupStorageLocationsGVR] {
		if bsl.GetName() == field(repo, "spec", "backupStorageLocation") {
			return storage.Resolve(repo, &bsl)
		}
	}
	return storage.Target{}, fmt.Errorf("repository %q has no observed BSL", repo.GetName())
}

func intentSpec(obj *unstructured.Unstructured) (BackupRepositoryCleanupSpec, error) {
	var spec BackupRepositoryCleanupSpec
	raw, ok, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !ok {
		return spec, fmt.Errorf("missing cleanup spec")
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &spec); err != nil {
		return spec, err
	}
	expected, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
	if err != nil || !reflect.DeepEqual(raw, expected) {
		return spec, fmt.Errorf("unrecognized cleanup spec fields")
	}
	if spec.Repository.Name == "" || len(validation.IsDNS1123Subdomain(spec.Repository.Name)) != 0 ||
		spec.Repository.UID == "" || len(validation.IsDNS1123Subdomain(CleanupName(spec.Repository.UID))) != 0 ||
		obj.GetName() != CleanupName(spec.Repository.UID) || obj.GetNamespace() != Namespace ||
		HostedClusterNamespace(spec.Repository.VolumeNamespace) == "" || len(obj.GetOwnerReferences()) != 0 {
		return spec, fmt.Errorf("invalid cleanup identity or owner references")
	}
	if !strings.HasSuffix("/"+spec.Storage.Prefix, "/kopia/"+spec.Repository.VolumeNamespace+"/") {
		return spec, fmt.Errorf("cleanup prefix does not match volume namespace")
	}
	return spec, storage.Validate(spec.Storage)
}

func baseApply(gvr schema.GroupVersionResource, kind, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvr.GroupVersion().String(), "kind": kind,
		"metadata": map[string]interface{}{"name": name, "namespace": Namespace},
	}}
}

func intentApply(spec BackupRepositoryCleanupSpec) Apply {
	// The CRD must enforce whole-spec immutability. Fresh GETs detect existing
	// collisions, and that admission rule fences a racing create even when the
	// other writer did not claim SSA ownership. Never force an initial apply.
	obj := baseApply(BackupRepositoryCleanupsGVR, "BackupRepositoryCleanup", CleanupName(spec.Repository.UID))
	raw, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)
	obj.Object["spec"] = raw
	obj.SetFinalizers([]string{Finalizer})
	return Apply{Resource: BackupRepositoryCleanupsGVR, Object: obj, FieldManager: CleanupFieldManager}
}

func finalizerApply(obj *unstructured.Unstructured, gvr schema.GroupVersionResource, manager string, retain bool) Apply {
	patch := baseApply(gvr, obj.GetKind(), obj.GetName())
	patch.SetUID(obj.GetUID())
	patch.SetResourceVersion(obj.GetResourceVersion())
	if retain {
		patch.SetFinalizers([]string{Finalizer})
	}
	if gvr == BackupRepositoryCleanupsGVR {
		// This manager also owns the immutable spec; omission would delete it.
		patch.Object["spec"] = runtime.DeepCopyJSONValue(obj.Object["spec"])
	}
	return Apply{Resource: gvr, Object: patch, FieldManager: manager}
}

// finalizerOwnership does not adopt another manager's ownership. Applying an
// already-present value can co-own it, but omission cannot remove a co-owned
// value. Lost managedFields trigger a narrow reapply; if the API attributes the
// persisted value to before-first-apply, subsequent reconciliation blocks for
// operator repair. There is no force or whole-finalizer-list patch fallback.
// Dedicated managers must remain stable and must never own unrelated fields.
func finalizerOwnership(obj *unstructured.Unstructured, manager string) (ours, other bool) {
	for _, entry := range obj.GetManagedFields() {
		if entry.FieldsV1 == nil || entry.Subresource != "" {
			continue
		}
		var fields map[string]interface{}
		if json.Unmarshal(entry.FieldsV1.Raw, &fields) != nil {
			other = true
			continue
		}
		finalizers, found, _ := unstructured.NestedMap(fields, "f:metadata", "f:finalizers")
		if !found {
			continue
		}
		_, owns := finalizers[`v:"`+Finalizer+`"`]
		if entry.Manager == manager {
			for name := range finalizers {
				if name != "." && name != `v:"`+Finalizer+`"` {
					// Omission would also prune another set member previously
					// claimed by this manager, so do not attempt a release.
					other = true
				}
			}
		}
		// Atomic/list-level ownership cannot safely be pruned as a set member.
		if len(finalizers) == 0 {
			other = true
		}
		if owns {
			if entry.Manager == manager && entry.Operation == metav1.ManagedFieldsOperationApply {
				ours = true
			} else {
				other = true
			}
		}
	}
	return
}

func blockedPlan(o Observations, reason, message string) Plan {
	return conditionPlan(o, reason, message, metav1.ConditionFalse)
}

func conditionPlan(o Observations, reason, message string, status metav1.ConditionStatus) Plan {
	p := Plan{RequeueAfter: recheckInterval}
	condition := metav1.Condition{Type: Complete, Status: status, Reason: reason, Message: message,
		LastTransitionTime: metav1.NewTime(o.Now)}
	p.Condition = &condition
	if o.Cleanup == nil {
		return p
	}
	condition.ObservedGeneration = o.Cleanup.GetGeneration()
	if status == metav1.ConditionTrue && o.Cleanup.GetDeletionTimestamp() == nil {
		p.RequeueAfter = tombstoneRecheckInterval
	}
	conditions, _, _ := unstructured.NestedSlice(o.Cleanup.Object, "status", "conditions")
	for _, raw := range conditions {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		var old metav1.Condition
		if runtime.DefaultUnstructuredConverter.FromUnstructured(m, &old) == nil && old.Type == Complete {
			if old.Status == status {
				condition.LastTransitionTime = old.LastTransitionTime
			}
			if reflect.DeepEqual(old, condition) {
				p.Condition = &condition
				return p
			}
		}
	}
	p.Condition = &condition
	obj := baseApply(BackupRepositoryCleanupsGVR, "BackupRepositoryCleanup", o.Cleanup.GetName())
	obj.SetUID(o.Cleanup.GetUID())
	obj.SetResourceVersion(o.Cleanup.GetResourceVersion())
	raw, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&condition)
	obj.Object["status"] = map[string]interface{}{"conditions": []interface{}{raw}}
	p.Applies = []Apply{{Resource: BackupRepositoryCleanupsGVR, Object: obj, FieldManager: StatusFieldManager, Subresource: "status"}}
	return p
}

// PlanRepository attaches protection before requesting deletion. Terminating
// repositories not already protected are never retroactively finalized.
func PlanRepository(o Observations) (p Plan) {
	defer func() {
		// Only cleanup keys write intent status. Competing repo and cleanup keys
		// would otherwise alternate Blocked and WaitingForRepository forever.
		p.Applies = lifecycle(p).Applies
	}()
	r := o.Repository
	if r == nil {
		return Plan{}
	}
	if r.GetNamespace() != Namespace || r.GetUID() == "" || HostedClusterNamespace(field(r, "spec", "volumeNamespace")) == "" || field(r, "spec", "repositoryType") != "kopia" {
		return blockedPlan(o, Blocked, "Repository is not recognized ARO Kopia storage")
	}
	if preserved(r) || preserved(o.Cleanup) {
		return blockedPlan(o, Blocked, "Cleanup is preserved by annotation")
	}
	if r.GetDeletionTimestamp() != nil && !hasFinalizer(r) {
		return Plan{}
	}
	target, err := resolve(r, o.Objects)
	if err != nil {
		return blockedPlan(o, Blocked, err.Error())
	}
	ours, other := finalizerOwnership(r, RepositoryFieldManager)
	if !hasFinalizer(r) || !ours {
		if other {
			return blockedPlan(o, Blocked, "Repository finalizer is owned by another manager")
		}
		return Plan{Applies: []Apply{finalizerApply(r, BackupRepositoriesGVR, RepositoryFieldManager, true)}, RequeueAfter: recheckInterval}
	}
	if other {
		return blockedPlan(o, Blocked, "Repository finalizer has another owner; operator repair required")
	}
	if len(o.HostedClusters) > 0 {
		return blockedPlan(o, Blocked, "HostedCluster still exists in the base namespace")
	}
	if !o.DependenciesComplete {
		return Plan{ObserveDependencies: true}
	}
	spec := BackupRepositoryCleanupSpec{Repository: identity(r), Storage: target}
	if message := consumerBlock(o, spec, r); message != "" {
		return blockedPlan(o, Blocked, message)
	}
	if r.GetDeletionTimestamp() == nil {
		return Plan{Deletes: []Delete{deletion(r, BackupRepositoriesGVR)}, RequeueAfter: recheckInterval}
	}
	if o.Cleanup == nil {
		return Plan{Applies: []Apply{intentApply(spec)}, RequeueAfter: recheckInterval}
	}
	actual, err := intentSpec(o.Cleanup)
	if err != nil || actual != spec || o.Cleanup.GetDeletionTimestamp() != nil || !hasFinalizer(o.Cleanup) {
		return blockedPlan(o, Blocked, "Durable cleanup identity, target or finalizer does not match repository")
	}
	// The driver obtained this intent via a separate live GET on a subsequent
	// reconcile, not from an apply response or from its informer cache.
	return Plan{Applies: []Apply{finalizerApply(r, BackupRepositoriesGVR, RepositoryFieldManager, false)}, RequeueAfter: recheckInterval}
}

func deletion(obj *unstructured.Unstructured, gvr schema.GroupVersionResource) Delete {
	return Delete{Resource: gvr, Name: obj.GetName(), UID: obj.GetUID(), ResourceVersion: obj.GetResourceVersion()}
}

// PlanCleanup always rechecks live dependencies, even if status is Complete.
func PlanCleanup(o Observations) Plan {
	if o.Cleanup == nil {
		return Plan{}
	}
	spec, err := intentSpec(o.Cleanup)
	if err != nil {
		return blockedPlan(o, Blocked, err.Error())
	}
	if preserved(o.Cleanup) {
		return blockedPlan(o, Blocked, "Cleanup is preserved by annotation")
	}
	if !hasFinalizer(o.Cleanup) {
		if o.Cleanup.GetDeletionTimestamp() != nil {
			return blockedPlan(o, Blocked, "Terminating cleanup has no protection")
		}
		return Plan{Applies: []Apply{finalizerApply(o.Cleanup, BackupRepositoryCleanupsGVR, CleanupFieldManager, true)}, RequeueAfter: recheckInterval}
	}
	ours, other := finalizerOwnership(o.Cleanup, CleanupFieldManager)
	if other {
		return blockedPlan(o, Blocked, "Cleanup finalizer has another owner; operator repair required")
	}
	if !ours {
		return Plan{Applies: []Apply{finalizerApply(o.Cleanup, BackupRepositoryCleanupsGVR, CleanupFieldManager, true)}, RequeueAfter: recheckInterval}
	}
	if o.Repository != nil {
		return blockedPlan(o, WaitingForRepository, "Repository name is still present (any UID protects)")
	}
	if len(o.HostedClusters) > 0 {
		return blockedPlan(o, Blocked, "HostedCluster still exists in the base namespace")
	}
	if !o.DependenciesComplete {
		return Plan{ObserveDependencies: true}
	}
	if message := consumerBlock(o, spec, nil); message != "" {
		return blockedPlan(o, Blocked, message)
	}
	jobs, pods, message := maintenance(o, spec)
	if message != "" {
		return blockedPlan(o, DrainingMaintenance, message)
	}
	if len(jobs) > 0 {
		p := blockedPlan(o, DrainingMaintenance, "Removing terminal maintenance Jobs")
		if jobs[0].GetDeletionTimestamp() == nil {
			p.Deletes = []Delete{deletion(&jobs[0], JobsGVR)}
		}
		return p
	}
	if len(pods) > 0 {
		p := blockedPlan(o, DrainingMaintenance, "Removing terminal orphan maintenance Pods")
		if pods[0].GetDeletionTimestamp() == nil {
			p.Deletes = []Delete{deletion(&pods[0], PodsGVR)}
		}
		return p
	}
	if o.Swept == nil || *o.Swept != spec.Storage {
		p := blockedPlan(o, DeletingBlobs, "Sweeping the retired repository prefix")
		// An event caused by our successful status write must not flip Complete
		// back to DeletingBlobs and create an endless self-triggered sweep loop.
		conditions, _, _ := unstructured.NestedSlice(o.Cleanup.Object, "status", "conditions")
		for _, raw := range conditions {
			condition, ok := raw.(map[string]interface{})
			if !ok || condition["type"] != Complete {
				continue
			}
			if condition["status"] == "True" {
				p = Plan{RequeueAfter: tombstoneRecheckInterval}
			} else if condition["reason"] == SweepFailed {
				// Keep failures stable while retrying. Otherwise our own status
				// events bypass queue backoff by alternating progress and failure.
				p = Plan{RequeueAfter: recheckInterval}
			}
		}
		p.DeletePrefix = &spec.Storage
		return p
	}
	p := conditionPlan(o, CleanupComplete, "Prefix swept and live dependencies absent; tombstone remains monitored", metav1.ConditionTrue)
	if o.Cleanup.GetDeletionTimestamp() != nil {
		// Persist completion first. The next reconcile sweeps and verifies again
		// before release; a persisted condition alone never authorizes release.
		if len(p.Applies) != 0 {
			return p
		}
		p.Applies = []Apply{finalizerApply(o.Cleanup, BackupRepositoryCleanupsGVR, CleanupFieldManager, false)}
	}
	return p
}

func overlaps(a, b storage.Target) bool {
	return strings.TrimSuffix(a.AccountURL, "/") == strings.TrimSuffix(b.AccountURL, "/") && a.Container == b.Container && (strings.HasPrefix(a.Prefix, b.Prefix) || strings.HasPrefix(b.Prefix, a.Prefix))
}

var (
	azureAccount   = regexp.MustCompile(`^[a-z0-9]{3,24}$`)
	azureContainer = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	storageSegment = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// bslScope resolves only a public Azure account/container/base, independent of
// repository type or retirement namespace eligibility. It grants no deletion
// authority; unknown endpoint/configuration shapes cannot prove disjointness.
func bslScope(bsl *unstructured.Unstructured) (storage.Target, bool) {
	if field(bsl, "spec", "provider") != "azure" {
		return storage.Target{}, false
	}
	config, found, err := unstructured.NestedStringMap(bsl.Object, "spec", "config")
	if err != nil || !found || !azureAccount.MatchString(config["storageAccount"]) {
		return storage.Target{}, false
	}
	url := "https://" + config["storageAccount"] + ".blob.core.windows.net"
	for key, value := range config {
		switch key {
		case "storageAccount", "resourceGroup", "subscriptionId", "useAAD":
		case "storageAccountURI":
			if strings.TrimSuffix(value, "/") != url {
				return storage.Target{}, false
			}
		case "activeDirectoryAuthorityURI":
			if strings.TrimSuffix(value, "/") != "https://login.microsoftonline.com" {
				return storage.Target{}, false
			}
		default:
			return storage.Target{}, false
		}
	}
	container := field(bsl, "spec", "objectStorage", "bucket")
	if !azureContainer.MatchString(container) || strings.Contains(container, "--") {
		return storage.Target{}, false
	}
	base, _, err := unstructured.NestedString(bsl.Object, "spec", "objectStorage", "prefix")
	if err != nil {
		return storage.Target{}, false
	}
	base = strings.Trim(base, "/")
	if base != "" {
		for _, part := range strings.Split(base, "/") {
			if !storageSegment.MatchString(part) || part == "." || part == ".." {
				return storage.Target{}, false
			}
		}
		base += "/"
	}
	return storage.Target{AccountURL: url, Container: container, Prefix: base}, true
}

func consumerBlock(o Observations, spec BackupRepositoryCleanupSpec, retiring *unstructured.Unstructured) string {
	for _, gvr := range RequiredResources() {
		if gvr == HostedClustersGVR || gvr == BackupRepositoryCleanupsGVR {
			continue
		}
		if _, ok := o.Objects[gvr]; !ok {
			return "Incomplete observations for " + gvr.Resource
		}
	}
	if len(o.HostedClusters) > 0 {
		return "HostedCluster still exists in the base namespace"
	}
	for _, repo := range o.Objects[BackupRepositoriesGVR] {
		if retiring != nil && repo.GetUID() == retiring.GetUID() && repo.GetName() == retiring.GetName() {
			continue
		}
		if repo.GetName() == spec.Repository.Name {
			return "Repository name has been replaced"
		}
		// A positively non-Azure BSL cannot alias Azure. Everything ambiguous is
		// fail-closed, including missing BSLs and unsupported Azure repo shapes.
		disjoint := false
		for _, bsl := range o.Objects[BackupStorageLocationsGVR] {
			if bsl.GetName() == field(&repo, "spec", "backupStorageLocation") {
				provider := field(&bsl, "spec", "provider")
				disjoint = provider == "aws" || provider == "gcp"
				if scope, ok := bslScope(&bsl); ok && !overlaps(scope, spec.Storage) {
					disjoint = true
				}
			}
		}
		if disjoint {
			continue
		}
		target, err := resolve(&repo, o.Objects)
		if err != nil {
			return "Cannot exclude physical alias for repository " + repo.GetName() + ": " + err.Error()
		}
		if overlaps(target, spec.Storage) {
			return "Another repository references overlapping physical storage"
		}
	}
	for _, backup := range o.Objects[BackupsGVR] {
		if namespaceMayMatch(&backup, spec.Repository.VolumeNamespace, "spec") {
			return "Associated or unscoped Backup remains"
		}
	}
	for _, schedule := range o.Objects[SchedulesGVR] {
		if namespaceMayMatch(&schedule, spec.Repository.VolumeNamespace, "spec", "template") {
			return "Matching or unscoped Schedule remains"
		}
	}
	for _, gvr := range []schema.GroupVersionResource{DataUploadsGVR, DataDownloadsGVR, RestoresGVR, PodVolumeBackupsGVR, PodVolumeRestoresGVR} {
		for _, obj := range o.Objects[gvr] {
			if terminalOperation(&obj, gvr) {
				continue
			}
			// A restore can refer to an already-removed backup or remap its
			// namespaces. Without positive source evidence it protects all targets.
			if gvr == RestoresGVR || gvr == PodVolumeRestoresGVR {
				return "Active restore has no safely excludable source repository"
			}
			volume := field(&obj, "spec", "sourceNamespace")
			if volume == "" {
				volume = field(&obj, "spec", "pod", "namespace")
			}
			if volume == "" || volume == spec.Repository.VolumeNamespace {
				return "Active or unscoped " + gvr.Resource + " remains"
			}
		}
	}
	return ""
}

func namespaceMayMatch(obj *unstructured.Unstructured, volume string, path ...string) bool {
	path = append(slices.Clone(path), "includedNamespaces")
	namespaces, found, err := unstructured.NestedStringSlice(obj.Object, path...)
	if err != nil || !found || len(namespaces) == 0 {
		return true
	}
	for _, ns := range namespaces {
		if ns == "*" || ns == volume || len(validation.IsDNS1123Label(ns)) != 0 {
			return true
		}
	}
	return false
}

func terminalOperation(obj *unstructured.Unstructured, gvr schema.GroupVersionResource) bool {
	phase := field(obj, "status", "phase")
	if phase == "Completed" || phase == "Failed" {
		return true
	}
	if gvr == RestoresGVR {
		return phase == "PartiallyFailed" || phase == "FailedValidation"
	}
	return (gvr == DataUploadsGVR || gvr == DataDownloadsGVR) && phase == "Canceled"
}

func maintenance(o Observations, spec BackupRepositoryCleanupSpec) (jobs, pods []unstructured.Unstructured, message string) {
	label := RepositoryLabel(spec.Repository.Name)
	jobUIDs := map[string]bool{}
	jobNames := map[string]bool{}
	for _, job := range o.Objects[JobsGVR] {
		if job.GetLabels()[repoNameLabel] != label {
			// A running worker may still use the BSL's previous target. Current
			// BSL configuration cannot establish disjointness for active work.
			if maintenanceShape(&job, spec.Repository.VolumeNamespace, true) && !terminalJob(&job) {
				return nil, nil, "Active foreign maintenance Job may write this prefix"
			}
			// Never delete foreign Jobs. Terminal Job conditions do not excuse
			// active Pods: the complete Pod list is checked independently below.
			continue
		}
		if !maintenanceShape(&job, spec.Repository.VolumeNamespace, true) {
			return nil, nil, "Ambiguous repository-labeled Job shape"
		}
		if !terminalJob(&job) {
			return nil, nil, "Maintenance Job is not Complete or Failed"
		}
		jobs = append(jobs, job)
		jobUIDs[string(job.GetUID())] = true
		jobNames[job.GetName()] = true
	}
	for _, pod := range o.Objects[PodsGVR] {
		associated := pod.GetLabels()[repoNameLabel] == label || jobNames[pod.GetLabels()["job-name"]] || jobNames[pod.GetLabels()["batch.kubernetes.io/job-name"]]
		for _, owner := range pod.GetOwnerReferences() {
			associated = associated || (owner.Kind == "Job" && jobUIDs[string(owner.UID)])
		}
		if !associated {
			phase := field(&pod, "status", "phase")
			if maintenanceShape(&pod, spec.Repository.VolumeNamespace, false) && phase != "Succeeded" && phase != "Failed" {
				return nil, nil, "Active foreign maintenance Pod may write this prefix"
			}
			continue
		}
		if !maintenanceShape(&pod, spec.Repository.VolumeNamespace, false) {
			return nil, nil, "Ambiguous maintenance Pod shape"
		}
		phase := field(&pod, "status", "phase")
		if phase != "Succeeded" && phase != "Failed" {
			return nil, nil, "Maintenance Pod is not terminal (termination is not completion)"
		}
		pods = append(pods, pod)
	}
	return
}

func terminalJob(job *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(job.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]interface{})
		if ok && condition["status"] == "True" && (condition["type"] == "Complete" || condition["type"] == "Failed") {
			return true
		}
	}
	return false
}

func maintenanceShape(obj *unstructured.Unstructured, volume string, job bool) bool {
	path := []string{"spec"}
	if job {
		path = []string{"spec", "template", "spec"}
	}
	containers, _, err := unstructured.NestedSlice(obj.Object, append(slices.Clone(path), "containers")...)
	if err != nil || len(containers) != 1 {
		return false
	}
	container, ok := containers[0].(map[string]interface{})
	if !ok {
		return false
	}
	command, _, _ := unstructured.NestedStringSlice(container, "command")
	args, _, _ := unstructured.NestedStringSlice(container, "args")
	return container["name"] == "velero-repo-maintenance-container" && len(command) == 1 && command[0] == "/velero" &&
		len(args) > 0 && args[0] == "repo-maintenance" && slices.Contains(args, "--repo-name="+volume) && slices.Contains(args, "--repo-type=kopia")
}
