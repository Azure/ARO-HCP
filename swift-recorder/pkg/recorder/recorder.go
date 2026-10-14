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

// Package recorder records bounded, node-local SWIFT startup episodes.
package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/swift-recorder/pkg/discovery"
)

const RecorderControllerName = "swift-startup-recorder"

const (
	identityLimit                     = 64
	identityTTL                       = 2 * time.Minute
	swiftResource corev1.ResourceName = "aro.openshift.io/swift-nic"
)

type Config struct {
	NodeName, ClusterName, Region, Environment, BootID, NetNSDir, Executable, CaptureMode string
	StartupDwell, PostSuccessCapture, SampleInterval, CaptureTimeout, EpisodeTimeout      time.Duration
	MaxPods, MaxBufferBytes, MaxRecordBytes                                               int
}

type Key struct {
	Namespace, Name string
	UID             types.UID
}

var _ utils.LoggableKey = Key{}

func (k Key) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("pod_namespace", k.Namespace, "pod_name", k.Name, "pod_uid", string(k.UID))
}

type observation struct {
	seen time.Time
}

type pendingAttempt struct {
	attempt        discovery.Attempt
	first, expires time.Time
}

type episode struct {
	seen, dwell, deadline, recovered, nextCapture time.Time
	attempt                                       discovery.Attempt
	published, hasHash, recoveryRecorded          bool
	condition                                     corev1.ConditionStatus
	hash                                          [sha256.Size]byte
	buffer                                        []json.RawMessage
	bytes                                         int
}

type Controller struct {
	cfg            Config
	name           string
	pods           coreinformers.PodInformer
	handler        cache.ResourceEventHandlerRegistration
	queue          workqueue.TypedRateLimitingInterface[Key]
	captureFn      func(context.Context, string, string, int) (json.RawMessage, error)
	now            func() time.Time
	ready, started atomic.Bool

	// Only callback ingress is shared. All episode and buffer state belongs to Run's worker.
	mu          sync.Mutex
	observed    map[Key]observation
	pending     map[types.UID]pendingAttempt
	completed   map[Key]time.Time
	episodes    map[Key]*episode
	bufferBytes int
}

func New(cfg Config, pods coreinformers.PodInformer, captureFn func(context.Context, string, string, int) (json.RawMessage, error)) (*Controller, error) {
	if cfg.NodeName == "" || cfg.Executable == "" || pods == nil || captureFn == nil {
		return nil, fmt.Errorf("node name, executable, pod informer and capture function are required")
	}
	if cfg.CaptureMode != "slow" && cfg.CaptureMode != "all" {
		return nil, fmt.Errorf("capture mode must be slow or all")
	}
	if cfg.StartupDwell < 0 || cfg.PostSuccessCapture < 0 || cfg.SampleInterval <= 0 || cfg.CaptureTimeout <= 0 || cfg.EpisodeTimeout <= 0 {
		return nil, fmt.Errorf("dwell and post-success durations must be nonnegative; other durations must be positive")
	}
	if cfg.MaxPods <= 0 || cfg.MaxRecordBytes <= 0 || cfg.MaxBufferBytes/cfg.MaxPods < cfg.MaxRecordBytes {
		return nil, fmt.Errorf("positive limits must reserve at least one maximum record per pod")
	}
	if !filepath.IsAbs(cfg.NetNSDir) || filepath.Clean(cfg.NetNSDir) != cfg.NetNSDir || cfg.NetNSDir == "/" {
		return nil, fmt.Errorf("namespace directory must be clean, absolute and not root")
	}
	dir, err := filepath.EvalSymlinks(cfg.NetNSDir)
	if err != nil {
		return nil, fmt.Errorf("resolve namespace directory: %w", err)
	}
	cfg.NetNSDir = dir
	c := &Controller{
		cfg: cfg, name: RecorderControllerName, pods: pods, captureFn: captureFn, now: time.Now,
		observed: make(map[Key]observation), pending: make(map[types.UID]pendingAttempt),
		completed: make(map[Key]time.Time), episodes: make(map[Key]*episode),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(workqueue.DefaultTypedControllerRateLimiter[Key](), workqueue.TypedRateLimitingQueueConfig[Key]{Name: RecorderControllerName}),
	}
	c.handler, err = pods.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.observePod(nil, obj) },
		UpdateFunc: c.observePod,
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if pod, ok := obj.(*corev1.Pod); ok {
				key := podKey(pod)
				c.mu.Lock()
				_, tracked := c.observed[key]
				c.mu.Unlock()
				if tracked {
					c.queue.Add(key)
				}
			}
		},
	})
	if err != nil {
		c.queue.ShutDown()
		return nil, err
	}
	return c, nil
}

func podKey(pod *corev1.Pod) Key {
	return Key{Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID}
}

func (c *Controller) eligible(pod *corev1.Pod) bool {
	if pod.UID == "" || pod.Spec.NodeName != c.cfg.NodeName || pod.Spec.HostNetwork || pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return false
	}
	for _, containers := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, container := range containers {
			for _, resources := range []corev1.ResourceList{container.Resources.Requests, container.Resources.Limits} {
				if quantity := resources[swiftResource]; quantity.Sign() > 0 {
					return true
				}
			}
		}
	}
	return false
}

func networkCondition(pod *corev1.Pod) (corev1.ConditionStatus, time.Time) {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReadyToStartContainers && (condition.Status == corev1.ConditionTrue || condition.Status == corev1.ConditionFalse) {
			return condition.Status, condition.LastTransitionTime.Time
		}
	}
	return corev1.ConditionUnknown, time.Time{}
}

func (c *Controller) candidate(pod *corev1.Pod, now time.Time) bool {
	if !c.eligible(pod) {
		return false
	}
	status, transition := networkCondition(pod)
	// An absolute startup age bound survives completion-cache eviction and recorder
	// restarts. Pod StartTime is useful here, but is not container recovery time.
	start := pod.CreationTimestamp.Time
	if pod.Status.StartTime != nil {
		start = pod.Status.StartTime.Time
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionTrue && !condition.LastTransitionTime.IsZero() {
			start = condition.LastTransitionTime.Time
		}
	}
	if !start.IsZero() && now.Sub(start) >= c.cfg.EpisodeTimeout {
		return false
	}
	// A recorder restart must not turn old running pods or sandbox restarts into startups.
	if pod.Status.Phase == corev1.PodRunning {
		started := containersStarted(pod)
		for _, container := range pod.Status.ContainerStatuses {
			if container.RestartCount > 0 || container.LastTerminationState.Terminated != nil {
				return false
			}
		}
		if status == corev1.ConditionFalse || (!started.IsZero() && now.Sub(started) > c.cfg.PostSuccessCapture) {
			return false
		}
		if status != corev1.ConditionTrue {
			return c.cfg.CaptureMode == "all" && !started.IsZero() && now.Sub(started) <= c.cfg.PostSuccessCapture
		}
	}
	return status == corev1.ConditionFalse || (status != corev1.ConditionTrue && pod.Status.Phase != corev1.PodRunning) ||
		(c.cfg.CaptureMode == "all" && status == corev1.ConditionTrue && !transition.IsZero() && now.Sub(transition) <= c.cfg.PostSuccessCapture)
}

func containersStarted(pod *corev1.Pod) time.Time {
	var first time.Time
	for _, container := range pod.Status.ContainerStatuses {
		var started time.Time
		if container.State.Running != nil {
			started = container.State.Running.StartedAt.Time
		} else if container.State.Terminated != nil {
			started = container.State.Terminated.StartedAt.Time
		}
		if !started.IsZero() && (first.IsZero() || started.Before(first)) {
			first = started
		}
	}
	return first
}

func (c *Controller) observePod(oldObj, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	key, now := podKey(pod), c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queue.ShuttingDown() {
		return
	}
	c.expire(now)
	if _, tracked := c.observed[key]; !tracked {
		if !c.candidate(pod, now) {
			delete(c.pending, key.UID)
			return
		}
		old, _ := oldObj.(*corev1.Pod)
		// Updates only admit newly assigned pods, never a new sandbox on a running pod.
		if old != nil && old.UID == pod.UID && old.Spec.NodeName == c.cfg.NodeName {
			return
		}
		if !c.admit(key, now) {
			return
		}
	}
	c.queue.Add(key)
}

// expire and admit are called with mu held; caches are bounded even without traffic.
func (c *Controller) expire(now time.Time) {
	for uid, pending := range c.pending {
		if !now.Before(pending.expires) {
			delete(c.pending, uid)
		}
	}
	for key, expiry := range c.completed {
		if !now.Before(expiry) {
			delete(c.completed, key)
		}
	}
}

func (c *Controller) admit(key Key, now time.Time) bool {
	if _, done := c.completed[key]; done {
		return false
	}
	if len(c.observed) >= c.cfg.MaxPods {
		overflows.WithLabelValues("pods").Inc()
		return false
	}
	c.observed[key] = observation{seen: now}
	return true
}

// ObserveAttempt is nonblocking with respect to captures. Only the latest ADD per
// UID is retained, including ADDs whose pod has not reached the informer yet.
func (c *Controller) ObserveAttempt(attempt discovery.Attempt) {
	if attempt.PodUID == "" || attempt.PodName == "" || attempt.Namespace == "" || len(attempt.PodUID) > 128 || len(attempt.PodName) > 253 || len(attempt.Namespace) > 253 || len(attempt.SandboxID) > 256 || len(attempt.NetNS) > 4096 {
		return
	}
	now := c.now()
	key := Key{Namespace: attempt.Namespace, Name: attempt.PodName, UID: types.UID(attempt.PodUID)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queue.ShuttingDown() {
		return
	}
	c.expire(now)
	if _, done := c.completed[key]; done {
		return
	}
	// Read under mu so a racing pod callback either follows this insertion or
	// has already made the pod visible. Unknown ADDs never occupy queue slots.
	pod, err := c.pods.Lister().Pods(key.Namespace).Get(key.Name)
	_, tracked := c.observed[key]
	if err == nil && (pod.UID != key.UID || !c.eligible(pod) || (!tracked && !c.candidate(pod, now))) {
		delete(c.pending, key.UID)
		return
	}
	previous, exists := c.pending[key.UID]
	if exists && (previous.attempt.Namespace != key.Namespace || previous.attempt.PodName != key.Name) {
		return
	}
	if !exists && len(c.pending) >= identityLimit {
		overflows.WithLabelValues("attempts").Inc()
		return
	}
	if exists && !attempt.ObservedAt.IsZero() && attempt.ObservedAt.Before(previous.attempt.ObservedAt) {
		return
	}
	if attempt.ObservedAt.IsZero() || attempt.ObservedAt.After(now) {
		attempt.ObservedAt = now
	}
	if now.Sub(attempt.ObservedAt) >= identityTTL {
		return
	}
	// Admission reserves a slot until the worker finishes, even if pending expires.
	if err == nil && !tracked && !c.admit(key, now) {
		return
	}
	first := previous.first
	if !exists {
		first = attempt.ObservedAt
	}
	c.pending[key.UID] = pendingAttempt{attempt: attempt, first: first, expires: now.Add(identityTTL)}
	if err == nil {
		c.queue.Add(key)
	}
}

func (c *Controller) Ready() bool { return c.ready.Load() }

// Run owns the sole worker. The caller starts the shared pod informer.
func (c *Controller) Run(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return fmt.Errorf("recorder can only run once")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.queue.ShutDown()
		clear(c.observed)
		clear(c.pending)
		clear(c.completed)
		clear(c.episodes)
		c.bufferBytes = 0
	}()
	defer c.ready.Store(false)
	defer func() { _ = c.pods.Informer().RemoveEventHandler(c.handler) }()
	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	logger = logger.WithValues("node_name", c.cfg.NodeName, "cluster_name", c.cfg.ClusterName, "region", c.cfg.Region, "environment", c.cfg.Environment, "boot_id", c.cfg.BootID, "capture_mode", c.cfg.CaptureMode)
	ctx = utils.ContextWithLogger(ctx, logger)
	if !cache.WaitForCacheSync(ctx.Done(), c.pods.Informer().HasSynced, c.handler.HasSynced) {
		return ctx.Err()
	}
	go func() {
		defer utilruntime.HandleCrash()
		ticker := time.NewTicker(identityTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				c.queue.ShutDown()
				return
			case <-ticker.C:
				c.mu.Lock()
				c.expire(c.now())
				c.mu.Unlock()
			}
		}
	}()
	c.ready.Store(true)
	for c.processNext(ctx) {
	}
	return ctx.Err()
}

func (c *Controller) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)
	defer c.queue.Forget(key)
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key)
	ctx = utils.ContextWithLogger(ctx, logger)
	if ctx.Err() == nil {
		c.sync(ctx, key)
	}
	return true
}

func (c *Controller) sync(ctx context.Context, key Key) {
	now := c.now()
	c.mu.Lock()
	c.expire(now)
	_, tracked := c.observed[key]
	c.mu.Unlock()
	pod, err := c.pods.Lister().Pods(key.Namespace).Get(key.Name)
	if apierrors.IsNotFound(err) && !tracked {
		// The ADD can precede informer visibility; keep its identity until TTL.
		return
	}
	if err != nil && !apierrors.IsNotFound(err) {
		// Lister errors, like capture failures, never create unbounded rate-limit retries.
		if e := c.episodes[key]; e != nil && now.Before(e.deadline) {
			c.queue.AddAfter(key, c.cfg.SampleInterval)
		} else {
			c.finish(ctx, key, "pod_unavailable")
		}
		return
	}
	if err != nil || pod.UID != key.UID || !c.eligible(pod) {
		c.finish(ctx, key, "deleted_or_ineligible")
		return
	}
	c.mu.Lock()
	observed, tracked := c.observed[key]
	pending, hasAttempt := c.pending[key.UID]
	hasAttempt = hasAttempt && pending.attempt.Namespace == key.Namespace && pending.attempt.PodName == key.Name
	if !tracked && hasAttempt && c.candidate(pod, now) {
		tracked = c.admit(key, now)
		observed = c.observed[key]
	}
	if !tracked && hasAttempt {
		delete(c.pending, key.UID)
	}
	c.mu.Unlock()
	if !tracked {
		return
	}
	status, transition := networkCondition(pod)
	e := c.episodes[key]
	if e == nil {
		seen := observed.seen
		if hasAttempt && pending.first.Before(seen) {
			seen = pending.first
		}
		e = &episode{seen: seen, dwell: seen.Add(c.cfg.StartupDwell), deadline: seen.Add(c.cfg.EpisodeTimeout), condition: status}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionTrue && !condition.LastTransitionTime.IsZero() && condition.LastTransitionTime.Time.Before(seen) {
				// Account for scheduling delay, but never backdate the bounded episode lifetime.
				e.dwell = condition.LastTransitionTime.Add(c.cfg.StartupDwell)
			}
		}
		if hasAttempt {
			e.attempt = pending.attempt
		}
		c.episodes[key] = e
		episodes.WithLabelValues("started").Inc()
		c.record(ctx, key, e, "open", "", nil)
	}
	if hasAttempt {
		e.attempt = pending.attempt
	}
	e.condition = status
	if !now.Before(e.deadline) {
		c.finish(ctx, key, "timeout")
		return
	}
	healthy := status == corev1.ConditionTrue || (status != corev1.ConditionFalse && pod.Status.Phase == corev1.PodRunning)
	if healthy && e.recovered.IsZero() {
		if status != corev1.ConditionTrue {
			transition = containersStarted(pod)
		}
		e.recovered = now
		if !transition.IsZero() && transition.Before(now) {
			e.recovered = transition
		}
		if c.cfg.CaptureMode == "slow" && !e.recovered.After(e.dwell) {
			c.finish(ctx, key, "healthy_before_dwell")
			return
		}
	}
	if !e.published && (c.cfg.CaptureMode == "all" || !now.Before(e.dwell)) {
		c.publish(ctx, e)
	}
	if healthy && !e.recovered.IsZero() {
		// Only the first recovery gets an event and starts the fixed post-success window.
		if !e.recoveryRecorded {
			c.record(ctx, key, e, "recovery", "", nil)
			e.recoveryRecorded = true
		}
	}
	// A later readiness regression or sandbox restart cannot extend this startup.
	if !e.recovered.IsZero() && !now.Before(e.recovered.Add(c.cfg.PostSuccessCapture)) {
		c.finish(ctx, key, "recovered")
		return
	}
	if !now.Before(e.nextCapture) {
		c.capture(ctx, key, e)
		e.nextCapture = c.now().Add(c.cfg.SampleInterval)
	}
	if ctx.Err() != nil {
		return
	}
	next := min(c.cfg.SampleInterval, e.deadline.Sub(c.now()))
	if !e.published && e.dwell.After(c.now()) {
		next = min(next, e.dwell.Sub(c.now()))
	}
	if !e.recovered.IsZero() {
		next = min(next, e.recovered.Add(c.cfg.PostSuccessCapture).Sub(c.now()))
	}
	c.queue.AddAfter(key, max(next, 0))
}

// namespacePath only accepts the configured directory and the standard /var/run
// alias. Resolving an arbitrary source symlink is deliberately not sufficient.
func (c *Controller) namespacePath(path string) (string, error) {
	base, parent := filepath.Base(path), filepath.Dir(path)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.HasPrefix(base, "cni-") || len(base) <= 4 {
		return "", fmt.Errorf("invalid namespace path")
	}
	if parent != c.cfg.NetNSDir && (c.cfg.NetNSDir != "/run/netns" || parent != "/var/run/netns") {
		return "", fmt.Errorf("namespace outside configured directory")
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != c.cfg.NetNSDir {
		return "", fmt.Errorf("namespace parent does not match configured directory")
	}
	return filepath.Join(resolved, base), nil
}

func (c *Controller) capture(ctx context.Context, key Key, e *episode) {
	path, err := c.namespacePath(e.attempt.NetNS)
	reason := "invalid_namespace"
	if e.attempt.NetNS == "" {
		reason = "attempt_unavailable"
	}
	var data json.RawMessage
	started := c.now()
	if err == nil {
		reason = "capture_unavailable"
		captureCtx, cancel := context.WithTimeout(ctx, min(c.cfg.CaptureTimeout, e.deadline.Sub(started)))
		data, err = c.captureFn(captureCtx, c.cfg.Executable, path, c.cfg.MaxRecordBytes)
		cancel()
	}
	if ctx.Err() != nil {
		return
	}
	if len(data) > c.cfg.MaxRecordBytes {
		overflows.WithLabelValues("record").Inc()
		err = fmt.Errorf("oversized capture JSON")
		reason = "record_too_large"
	}
	if err == nil && !json.Valid(data) {
		err = fmt.Errorf("invalid or oversized capture JSON")
		reason = "invalid_json"
	}
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			reason = "capture_timeout"
		case errors.Is(err, os.ErrPermission):
			reason = "permission_denied"
		case errors.Is(err, os.ErrNotExist):
			reason = "not_found"
		case errors.As(err, &exitErr):
			reason = "helper_failed"
		}
		e.hasHash = false
		captures.WithLabelValues("error").Inc()
		// Do not copy helper stderr (or arbitrary errors) into records.
		c.record(ctx, key, e, "error", reason, nil)
		return
	}
	captures.WithLabelValues("success").Inc()
	// Include sandbox identity: identical state in a replacement namespace is new evidence.
	hash := sha256.Sum256(append([]byte(e.attempt.SandboxID+"\x00"+path+"\x00"), data...))
	if e.hasHash && e.hash == hash {
		captures.WithLabelValues("deduplicated").Inc()
		return
	}
	if c.record(ctx, key, e, "snapshot", "", &snapshot{StartedAt: started, FinishedAt: c.now(), State: data}) {
		e.hash, e.hasHash = hash, true
	}
}

type snapshot struct {
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	State      json.RawMessage `json:"state"`
}

func (c *Controller) record(ctx context.Context, key Key, e *episode, event, reason string, data *snapshot) bool {
	record := struct {
		Event            string                 `json:"event"`
		At               time.Time              `json:"at"`
		EpisodeStartedAt time.Time              `json:"episode_started_at"`
		PodUID           types.UID              `json:"pod_uid"`
		SandboxID        string                 `json:"sandbox_id,omitempty"`
		NetNS            string                 `json:"netns_path,omitempty"`
		ADDObservedAt    time.Time              `json:"add_observed_at,omitzero"`
		ADDSourceTime    time.Time              `json:"add_source_time,omitzero"`
		Reason           string                 `json:"reason,omitempty"`
		NetworkCondition corev1.ConditionStatus `json:"network_condition"`
		Snapshot         *snapshot              `json:"snapshot,omitempty"`
	}{event, c.now(), e.seen, key.UID, e.attempt.SandboxID, e.attempt.NetNS, e.attempt.ObservedAt, e.attempt.SourceTime, reason, e.condition, data}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > c.cfg.MaxRecordBytes {
		overflows.WithLabelValues("record").Inc()
		return false
	}
	if e.published {
		utils.LoggerFromContext(ctx).Info("SWIFT startup record", "record", json.RawMessage(raw))
		return true
	}
	// Evict oldest evidence, retaining a byte-bounded rolling window until dwell.
	for len(e.buffer) > 0 && (e.bytes+len(raw) > c.cfg.MaxBufferBytes/c.cfg.MaxPods || c.bufferBytes+len(raw) > c.cfg.MaxBufferBytes) {
		overflows.WithLabelValues("buffer").Inc()
		n := len(e.buffer[0])
		e.buffer[0] = nil
		e.buffer = e.buffer[1:]
		e.bytes -= n
		c.bufferBytes -= n
	}
	e.buffer = append(e.buffer, raw)
	e.bytes += len(raw)
	c.bufferBytes += len(raw)
	return true
}

func (c *Controller) publish(ctx context.Context, e *episode) {
	e.published = true
	for _, raw := range e.buffer {
		utils.LoggerFromContext(ctx).Info("SWIFT startup record", "record", raw)
	}
	c.bufferBytes -= e.bytes
	e.buffer, e.bytes = nil, 0
	episodes.WithLabelValues("published").Inc()
}

func (c *Controller) finish(ctx context.Context, key Key, reason string) {
	if e := c.episodes[key]; e != nil {
		c.record(ctx, key, e, "close", reason, nil)
		c.bufferBytes -= e.bytes
		delete(c.episodes, key)
		episodes.WithLabelValues(reason).Inc()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if pending, ok := c.pending[key.UID]; ok && pending.attempt.Namespace == key.Namespace && pending.attempt.PodName == key.Name {
		delete(c.pending, key.UID)
	}
	if _, tracked := c.observed[key]; !tracked {
		return
	}
	delete(c.observed, key)
	if len(c.completed) >= identityLimit {
		var oldest Key
		var expiry time.Time
		for candidate, until := range c.completed {
			if expiry.IsZero() || until.Before(expiry) {
				oldest, expiry = candidate, until
			}
		}
		delete(c.completed, oldest)
		overflows.WithLabelValues("completed").Inc()
	}
	c.completed[key] = c.now().Add(max(identityTTL, c.cfg.EpisodeTimeout))
}
