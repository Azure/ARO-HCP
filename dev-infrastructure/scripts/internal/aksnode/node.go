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

// Package aksnode provides Kubernetes node readiness checks and drain helpers
// for ARO HCP pipeline scripts.
package aksnode

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	armcs "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

const pollIntervalSec = 15

// IsReady reports whether the node has the NodeReady condition set to True.
func IsReady(n *corev1.Node) bool {
	if n == nil {
		return false
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// IsSchedulableReady reports whether the node is ready, schedulable, and not
// being deleted.
func IsSchedulableReady(n *corev1.Node) bool {
	if !IsReady(n) {
		return false
	}
	if n.Spec.Unschedulable {
		return false
	}
	return n.DeletionTimestamp == nil
}

// AllExpectedReady is a hard safety gate used before any destructive pool
// operation. It returns an error (with no side effects) if any expected pool
// is absent from livePools, not in provisioningState "Succeeded", or has fewer
// than minReadyNodes schedulable-ready Kubernetes nodes.
//
// Pass the live pool map from aksclient.ListPoolsByNodeLabel so the ARM list
// only happens once per run.
func AllExpectedReady(
	ctx context.Context,
	kube kubernetes.Interface,
	expectedNames []string,
	livePools map[string]*armcs.AgentPool,
	minReadyNodes int,
	readyTimeout time.Duration,
) error {
	for _, name := range expectedNames {
		pool, ok := livePools[name]
		if !ok {
			return fmt.Errorf("expected pool %q not found in live state — aborting to avoid unsafe operation", name)
		}
		if pool.Properties == nil {
			return fmt.Errorf("expected pool %q has nil properties — aborting", name)
		}
		ps := derefStr(pool.Properties.ProvisioningState)
		if ps != "Succeeded" {
			return fmt.Errorf("expected pool %q has provisioningState=%q (want Succeeded) — aborting", name, ps)
		}
		if err := WaitForReady(ctx, kube, name, minReadyNodes, readyTimeout); err != nil {
			return fmt.Errorf("expected pool %q failed readiness check: %w — aborting", name, err)
		}
	}
	return nil
}

// WaitForReady polls until at least want schedulable-ready nodes exist in the
// given pool, or the timeout expires.
func WaitForReady(ctx context.Context, kube kubernetes.Interface, pool string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollIntervalSec * time.Second)
	defer ticker.Stop()
	for {
		nodes, err := kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: "agentpool=" + pool,
		})
		if err != nil {
			slog.Warn("list nodes", "pool", pool, "err", err)
		} else {
			ready := 0
			for _, n := range nodes.Items {
				if IsSchedulableReady(&n) {
					ready++
				}
			}
			slog.Info("node readiness", "pool", pool, "ready", ready, "want", want)
			if ready >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pool %s did not reach %d ready node(s) within %s", pool, want, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Drain cordons and drains all nodes in the pool. Drain hiccups are logged
// but not fatal: the subsequent ARM pool delete will force-evict remaining pods.
func Drain(ctx context.Context, kube kubernetes.Interface, pool string, timeout time.Duration) error {
	nodes, err := kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "agentpool=" + pool,
	})
	if err != nil {
		return fmt.Errorf("list nodes for pool %s: %w", pool, err)
	}
	if len(nodes.Items) == 0 {
		slog.Info("no nodes to drain", "pool", pool)
		return nil
	}

	var stdout, stderr bytes.Buffer
	drainer := &drain.Helper{
		Ctx:                 ctx,
		Client:              kube,
		Force:               true,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Timeout:             timeout,
		Out:                 &stdout,
		ErrOut:              &stderr,
	}
	for _, n := range nodes.Items {
		name := n.Name
		slog.Info("cordoning", "node", name)
		if err := drain.RunCordonOrUncordon(drainer, n.DeepCopy(), true); err != nil {
			return fmt.Errorf("cordon %s: %w", name, err)
		}
		slog.Info("draining", "node", name, "timeout", timeout.String())
		podList, errs := drainer.GetPodsForDeletion(name)
		for _, e := range errs {
			slog.Warn("inspect pods", "node", name, "err", e)
		}
		if podList == nil {
			continue
		}
		if w := podList.Warnings(); w != "" {
			slog.Warn("drain warnings", "node", name, "warnings", w)
		}
		if err := drainer.DeleteOrEvictPods(podList.Pods()); err != nil {
			slog.Warn("drain hiccup — pool delete will force-evict", "node", name, "err", err)
		}
	}
	logMultiline("drain/stdout", stdout.String())
	logMultiline("drain/stderr", stderr.String())
	return nil
}

func logMultiline(label, value string) {
	for _, line := range bytes.Split(bytes.TrimSpace([]byte(value)), []byte("\n")) {
		if len(line) > 0 {
			slog.Info(label, "line", string(line))
		}
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
