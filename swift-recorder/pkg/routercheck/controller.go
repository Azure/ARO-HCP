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

package routercheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/probe"
)

const RouterCheckControllerName = "swift-router-check"

type Config struct {
	NodeName, ClusterName, Region, Environment, BootID, Executable string
	MaxRecordBytes                                                 int
}

type Key struct {
	Namespace, Name string
	UID             types.UID
}

var _ utils.LoggableKey = Key{}

func (k Key) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("pod_namespace", k.Namespace, "pod_name", k.Name, "pod_uid", string(k.UID))
}

type ExecuteFunc func(context.Context, string, probe.Request) (json.RawMessage, error)

type Controller struct {
	cfg            Config
	name           string
	pods           coreinformers.PodInformer
	discovery      *discoverySources
	runtime        Runtime
	execute        ExecuteFunc
	handler        cache.ResourceEventHandlerRegistration
	queue          workqueue.TypedRateLimitingInterface[Key]
	ready, started atomic.Bool
	cooldown       *controllerutils.SettableCooldownChecker
	clock          clock.PassiveClock
}

func New(cfg Config, pods coreinformers.PodInformer, client kubernetes.Interface, dynamicClient dynamic.Interface, runtime Runtime, execute ExecuteFunc) (*Controller, error) {
	if cfg.NodeName == "" || cfg.Executable == "" || cfg.MaxRecordBytes < 1024 || pods == nil || client == nil || dynamicClient == nil || runtime == nil || execute == nil {
		return nil, fmt.Errorf("router check configuration incomplete")
	}
	for _, identity := range []string{cfg.NodeName, cfg.ClusterName, cfg.Region, cfg.Environment, cfg.BootID} {
		if len(identity) > 256 {
			return nil, fmt.Errorf("router check log identity exceeds 256 bytes")
		}
	}
	c := &Controller{cfg: cfg, name: RouterCheckControllerName, pods: pods, runtime: runtime, execute: execute,
		discovery: newDiscoverySources(client, dynamicClient), cooldown: controllerutils.NewSettableCooldownChecker(), clock: clock.RealClock{},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[Key](), workqueue.TypedRateLimitingQueueConfig[Key]{Name: RouterCheckControllerName})}
	var err error
	c.handler, err = pods.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.enqueue, UpdateFunc: func(_, obj any) { c.enqueue(obj) }, DeleteFunc: c.enqueue,
	})
	if err != nil {
		c.queue.ShutDown()
		return nil, err
	}
	return c, nil
}

func keyFor(p *corev1.Pod) Key { return Key{Namespace: p.Namespace, Name: p.Name, UID: p.UID} }
func (c *Controller) eligible(p *corev1.Pod) bool {
	return swiftRouter(p) && p.Spec.NodeName == c.cfg.NodeName && p.DeletionTimestamp == nil && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed
}

func (c *Controller) enqueue(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	if pod, ok := obj.(*corev1.Pod); ok {
		c.queue.Add(keyFor(pod))
	}
}

// Ready reports informer synchronization, not successful runtime or network checks.
func (c *Controller) Ready() bool { return c.ready.Load() }

// Run starts workers after all Kubernetes sources and the pod handler synchronize.
func (c *Controller) Run(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return fmt.Errorf("router controller can only run once")
	}
	defer c.queue.ShutDown()
	defer c.ready.Store(false)
	defer func() { _ = c.pods.Informer().RemoveEventHandler(c.handler) }()
	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	logger = logger.WithValues("node_name", c.cfg.NodeName, "cluster_name", c.cfg.ClusterName, "region", c.cfg.Region, "environment", c.cfg.Environment, "boot_id", c.cfg.BootID)
	ctx = utils.ContextWithLogger(ctx, logger)
	c.discovery.Start(ctx.Done())
	defer c.discovery.Shutdown()
	synced := append(c.discovery.HasSynced(), c.pods.Informer().HasSynced, c.handler.HasSynced)
	if !cache.WaitForCacheSync(ctx.Done(), synced...) {
		return ctx.Err()
	}
	c.ready.Store(true)
	var wg sync.WaitGroup
	for range 4 {
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
	return ctx.Err()
}

func (c *Controller) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)
	if ctx.Err() != nil {
		c.queue.Forget(key)
		return false
	}
	delay, err := c.sync(ctx, key)
	if err != nil {
		logger.Error(err, "SWIFT router check failed")
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	if delay > 0 {
		c.queue.AddAfter(key, delay)
	}
	return true
}

// Cadence is measured from completion, with positive 20% jitter: 30-36s
// during the first ten minutes, then 5-6m. Unknown creation time uses 5-6m.
func probeCadence(pod *corev1.Pod, now time.Time) time.Duration {
	if !pod.CreationTimestamp.IsZero() && now.Sub(pod.CreationTimestamp.Time) < 10*time.Minute {
		return 30 * time.Second
	}
	return 5 * time.Minute
}

type passSummary struct {
	Outcome        string         `json:"outcome"`
	Unavailable    string         `json:"unavailable,omitempty"`
	Partial        bool           `json:"partial"`
	Canceled       bool           `json:"canceled"`
	AgeSeconds     *int64         `json:"age_seconds"`
	CadenceSeconds int64          `json:"cadence_seconds"`
	ElapsedMS      int64          `json:"elapsed_ms"`
	Discovered     int            `json:"discovered_targets"`
	Submitted      int            `json:"submitted_targets"`
	Reported       int            `json:"reported_targets"`
	Omitted        int            `json:"omitted_targets"`
	Roles          map[string]int `json:"roles"`
	HTTPSuccess    int            `json:"http_success"`
	HTTPFailed     int            `json:"http_failed"`
	TCPConnected   int            `json:"tcp_connected"`
	TCPRefused     int            `json:"tcp_refused"`
	TCPTimeout     int            `json:"tcp_timeout"`
	TCPFailed      int            `json:"tcp_failed"`
	RetiredFailed  int            `json:"retired_failed"`
	DNSSuccess     int            `json:"dns_success"`
	DNSFailed      int            `json:"dns_failed"`
	DNSReported    int            `json:"dns_reported"`
	DNSExpected    int            `json:"dns_expected"`
	Issues         map[string]int `json:"issues,omitempty"`
	Truncated      int            `json:"truncated"`
	EvidenceErrors int            `json:"evidence_errors"`
}

var (
	errPodChanged         = errors.New("pod changed during probe pass")
	errSandboxChanged     = errors.New("sandbox changed during probe pass")
	errProbeResultInvalid = errors.New("invalid probe result")
	errProbeFailed        = errors.New("network probes failed")
)

func (c *Controller) sync(ctx context.Context, key Key) (delay time.Duration, passErr error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pod, err := c.pods.Lister().Pods(key.Namespace).Get(key.Name)
	if apierrors.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if pod.UID != key.UID || !c.eligible(pod) {
		return 0, nil
	}
	if delay := c.cooldown.TimeUntilReady(key); delay > 0 {
		return delay, nil
	}
	logger := utils.LoggerFromContext(ctx).WithValues("pass_id", uuid.NewString(), "pod_ready", podReady(pod), "pod_deleting", pod.DeletionTimestamp != nil, "pni", pod.Labels[pniLabel])
	ctx = utils.ContextWithLogger(ctx, logger)
	started := time.Now()
	summary := passSummary{Roles: map[string]int{}, Issues: map[string]int{}}
	var d Discovery
	var sandbox Sandbox
	var result *probe.Result
	// Detailed evidence accompanies failed passes; healthy passes emit a summary.
	defer func() {
		summary.Discovered = len(d.Targets)
		for _, target := range d.Targets {
			summary.Roles[target.Target.Role]++
		}
		summary.ElapsedMS = time.Since(started).Milliseconds()
		summary.CadenceSeconds = int64(probeCadence(pod, c.clock.Now()).Seconds())
		if !pod.CreationTimestamp.IsZero() {
			age := int64(c.clock.Now().Sub(pod.CreationTimestamp.Time).Seconds())
			summary.AgeSeconds = &age
		} else {
			summary.Issues["creation_timestamp_missing"]++
		}
		var runtimeErr *RuntimeError
		if errors.As(passErr, &runtimeErr) {
			summary.Unavailable = string(runtimeErr.Reason)
		}
		summary.Canceled = errors.Is(passErr, context.Canceled) || errors.Is(passErr, errSandboxChanged) || errors.Is(passErr, errPodChanged)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			summary.Unavailable = "pass_timeout"
		}
		failed := passErr != nil
		summary.Omitted = max(0, summary.Discovered-summary.Reported)
		summary.Partial = summary.Truncated > 0 || summary.EvidenceErrors > 0 || summary.DNSReported != summary.DNSExpected || summary.Omitted > 0 || summary.Unavailable != ""
		switch {
		case summary.Canceled:
			summary.Outcome = "canceled"
		case failed:
			summary.Outcome = "failed"
		case summary.Partial:
			summary.Outcome = "partial"
		case summary.RetiredFailed > 0:
			summary.Outcome = "advisory"
		default:
			summary.Outcome = "healthy"
		}
		if failed && !summary.Canceled {
			if summary.Unavailable != "" {
				c.record(ctx, "unavailable", summary.Unavailable)
			}
			for _, iface := range d.Swift {
				c.record(ctx, "swift_interface", iface)
			}
			c.record(ctx, "plan", map[string]any{"discovered_targets": summary.Discovered, "submitted_targets": summary.Submitted, "omitted_targets": summary.Discovered - summary.Submitted})
			for _, target := range d.Targets[:summary.Submitted] {
				c.record(ctx, "target", target)
			}
			// Batch omitted identities while retaining the existing record ceiling.
			var omitted []json.RawMessage
			batchBytes := 2
			for _, target := range d.Targets[summary.Submitted:] {
				raw, err := json.Marshal(target)
				if err != nil {
					continue
				}
				if len(omitted) > 0 && batchBytes+len(raw)+1+len("omitted_targets")+128 > min(c.cfg.MaxRecordBytes, 64*1024)/2 {
					c.record(ctx, "omitted_targets", omitted)
					omitted, batchBytes = nil, 2
				}
				omitted = append(omitted, raw)
				batchBytes += len(raw) + 1
			}
			if len(omitted) > 0 {
				c.record(ctx, "omitted_targets", omitted)
			}
			c.record(ctx, "dns_config", sandbox.DNS)
			c.record(ctx, "coverage", map[string]any{"discovered_targets": summary.Discovered, "submitted_targets": summary.Submitted, "reported_targets": summary.Reported, "omitted_targets": summary.Omitted, "partial": summary.Partial})
			if result != nil {
				c.record(ctx, "result", result)
			}
		}
		c.record(ctx, "summary", summary)
	}()
	sandbox, err = c.runtime.Sandbox(ctx, pod)
	if err != nil {
		summary.Unavailable = "runtime_discovery_failed"
		return 0, fmt.Errorf("discover runtime: %w", err)
	}
	logger = logger.WithValues("sandbox_id", sandbox.ID, "netns_path", sandbox.Path, "netns_device", sandbox.Device, "netns_inode", sandbox.Inode)
	ctx = utils.ContextWithLogger(ctx, logger)
	discoveryCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	d, err = Discover(discoveryCtx, c.discovery, pod, sandbox.DNS)
	stop()
	if err != nil {
		summary.Unavailable = "discovery_failed"
		return 0, fmt.Errorf("discover probe inputs: %w", err)
	}
	request := probe.Request{NamespacePath: sandbox.Path, NamespaceDevice: sandbox.Device, NamespaceInode: sandbox.Inode, DNS: sandbox.DNS, DNSNames: d.DNSNames, TrustBundles: d.TrustBundles}
	summary.DNSExpected = len(request.DNS.Servers) * len(request.DNSNames) * 4
	for _, iface := range d.Swift {
		request.SwiftIPs = append(request.SwiftIPs, iface.IP)
	}
	// Bound serialization with baseline checks ahead of worker fanout.
	submitted := min(len(d.Targets), probe.MaxTargets)
	summary.Submitted = submitted
	for _, target := range d.Targets[:submitted] {
		request.Targets = append(request.Targets, target.Target)
	}
	if submitted < len(d.Targets) {
		summary.Truncated++
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	// Revalidate identity after gathering inputs and while the helper runs.
	currentPod, err := c.pods.Lister().Pods(key.Namespace).Get(key.Name)
	if err != nil {
		return 0, err
	}
	if currentPod.UID != key.UID || !c.eligible(currentPod) {
		summary.Unavailable = "pod_changed"
		return 0, errPodChanged
	}
	current, err := c.runtime.Sandbox(ctx, currentPod)
	if err != nil {
		summary.Unavailable = "runtime_revalidation_failed"
		return 0, fmt.Errorf("revalidate runtime: %w", err)
	}
	if current.ID != sandbox.ID || current.Device != sandbox.Device || current.Inode != sandbox.Inode {
		summary.Unavailable = "sandbox_changed"
		return 0, errSandboxChanged
	}
	helperCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	done := make(chan error, 1)
	go func() {
		defer utilruntime.HandleCrash()
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-helperCtx.Done():
				return
			case <-ticker.C:
				current, err := c.runtime.Sandbox(helperCtx, pod)
				// Preserve the monitor's cause when it cancels the helper.
				if helperCtx.Err() != nil {
					return
				}
				if err != nil {
					done <- fmt.Errorf("monitor runtime: %w", err)
					stop()
					return
				}
				if current.ID != sandbox.ID || current.Device != sandbox.Device || current.Inode != sandbox.Inode {
					done <- errSandboxChanged
					stop()
					return
				}
			}
		}
	}()
	data, err := c.execute(helperCtx, c.cfg.Executable, request)
	helperErr := helperCtx.Err()
	stop()
	if err := <-done; err != nil {
		summary.Unavailable = "runtime_monitor_failed"
		if errors.Is(err, errSandboxChanged) {
			summary.Unavailable = "sandbox_changed"
		}
		return 0, err
	}
	if helperErr != nil {
		err = errors.Join(err, helperErr)
	}
	if err != nil {
		reason := "probe_execution_failed"
		if errors.Is(helperErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			reason = "probe_timeout"
		} else if errors.Is(err, context.Canceled) {
			reason = "probe_canceled"
		}
		summary.Unavailable = reason
		return 0, fmt.Errorf("execute probes: %w", err)
	}
	var decoded probe.Result
	if err := json.Unmarshal(data, &decoded); err != nil {
		summary.Unavailable = "probe_result_invalid"
		return 0, fmt.Errorf("decode probe result: %w", err)
	}
	if !summarizeResult(request, d.Targets[:submitted], decoded, &summary) {
		summary.Unavailable = "probe_result_invalid"
		return 0, errProbeResultInvalid
	}
	result = &decoded
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if summary.HTTPFailed+summary.TCPTimeout+summary.TCPFailed-summary.RetiredFailed+summary.DNSFailed > 0 {
		return 0, errProbeFailed
	}
	delay = wait.Jitter(probeCadence(pod, c.clock.Now()), 0.2)
	c.cooldown.SetCooldown(key, delay)
	return delay, nil
}

// Reject incomplete/mismatched helper payloads before trusting their outcomes.
func summarizeResult(request probe.Request, targets []TargetInfo, result probe.Result, summary *passSummary) bool {
	summary.Reported = len(result.Targets)
	summary.DNSReported = len(result.DNS)
	if result.NamespaceDevice != request.NamespaceDevice || result.NamespaceInode != request.NamespaceInode || result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) || len(result.Targets) != len(request.Targets) || len(targets) != len(request.Targets) {
		return false
	}
	for i, observation := range result.Targets {
		if observation.Target != request.Targets[i] || targets[i].Target != request.Targets[i] || observation.Stage == "" || observation.StartedAt.IsZero() {
			return false
		}
	}
	// The engine bounds resolver/name fanout and reports omissions separately.
	type question struct{ server, name, protocol, kind string }
	questions := map[question]int{}
	for _, server := range request.DNS.Servers[:min(4, len(request.DNS.Servers))] {
		for _, name := range request.DNSNames[:min(8, len(request.DNSNames))] {
			for _, protocol := range []string{"udp", "tcp"} {
				for _, kind := range []string{"TypeA", "TypeAAAA"} {
					questions[question{server, name, protocol, kind}]++
				}
			}
		}
	}
	for _, observation := range result.DNS {
		q := question{observation.Server, observation.Name, observation.Protocol, observation.Type}
		if questions[q] == 0 || observation.Stage == "" || observation.StartedAt.IsZero() {
			return false
		}
		questions[q]--
		if questions[q] == 0 {
			delete(questions, q)
		}
	}
	if len(questions) != 0 {
		return false
	}
	for i, observation := range result.Targets {
		failures := summary.HTTPFailed + summary.TCPTimeout + summary.TCPFailed
		if observation.Target.TCPOnly {
			switch observation.TCPOutcome {
			case "connected":
				if observation.Error == "" && observation.Stage == "complete" {
					summary.TCPConnected++
				} else {
					summary.TCPFailed++
				}
			case "refused":
				summary.TCPRefused++
			case "timeout":
				summary.TCPTimeout++
			default:
				summary.TCPFailed++
			}
		} else if observation.Stage == "complete" && observation.Error == "" && observation.HTTPStatus == 200 && observation.Expected200 && (observation.Target.PlainHTTP || (observation.TLSVerify && observation.TLSVerified)) {
			summary.HTTPSuccess++
		} else {
			summary.HTTPFailed++
		}
		// Failures are advisory only when all identities for a target are retiring.
		retiring := targets[i].Deleting
		if len(targets[i].Workers) > 0 {
			retiring = true
			for _, worker := range targets[i].Workers {
				if !worker.MachineDeleting && !worker.AzureMachineDeleting {
					retiring = false
					break
				}
			}
		}
		if retiring && summary.HTTPFailed+summary.TCPTimeout+summary.TCPFailed > failures {
			summary.RetiredFailed++
		}
		if observation.RouteError != "" {
			summary.EvidenceErrors++
		}
		if observation.PreliminaryRouteError != "" {
			summary.EvidenceErrors++
		}
		if observation.BodyTruncated {
			summary.Truncated++
		}
	}
	for _, observation := range result.DNS {
		// NOERROR/empty AAAA is normal on IPv4-only services. Required A
		// records must actually answer; NOERROR alone is not resolution.
		if observation.Stage == "complete" && observation.Error == "" && observation.RCode == 0 && !observation.Truncated && (observation.Type == "TypeAAAA" || len(observation.Answers) > 0) {
			summary.DNSSuccess++
		} else {
			summary.DNSFailed++
		}
	}
	summary.Truncated += len(result.Truncated) + len(result.Before.Truncated) + len(result.After.Truncated)
	summary.EvidenceErrors += len(result.Before.Errors) + len(result.After.Errors)
	return true
}

// Split structured results into independently useful JSON records, reserving
// half the ingestion ceiling for log fields, timestamps and source information.
func (c *Controller) record(ctx context.Context, field string, value any) {
	limit := min(c.cfg.MaxRecordBytes, 64*1024) / 2
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	if len(raw)+len(field)+128 <= limit {
		utils.LoggerFromContext(ctx).Info("SWIFT router check", "field", field, "record", json.RawMessage(raw))
		return
	}
	// Normalize structs and typed slices to permit schema-independent splitting.
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&normalized) != nil {
		return
	}
	switch values := normalized.(type) {
	case map[string]any:
		for key, item := range values {
			c.record(ctx, field+"."+key, item)
		}
	case []any:
		for i, item := range values {
			c.record(ctx, fmt.Sprintf("%s[%d]", field, i), item)
		}
	default:
		utils.LoggerFromContext(ctx).Info("SWIFT router check", "field", field, "record", "record_too_large")
	}
}
