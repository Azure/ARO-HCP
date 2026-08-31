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

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/drain"
)

const pollIntervalSec = 20

// drainPool cordons every node in `pool` before evicting pods. Cordon
// failure is fatal: if new pods can still land on the node, the drain
// phase is not reliable. Force=true additionally lets DeleteOrEvictPods
// remove unmanaged pods instead of getting stuck, but a failure evicting
// pods is still returned to the caller (not swallowed): whether that's
// fatal is a caller decision — the system pool caller treats it as fatal
// (stop, needs human review, before deleting a pool running
// CriticalAddonsOnly workloads), while the temp-pool cleanup caller treats
// it as non-fatal (log and proceed, since the pool delete itself is the
// authoritative cleanup either way).
func (c *clients) drainPool(ctx context.Context, pool string, timeout time.Duration) error {
	if c.kube == nil {
		return fmt.Errorf("kube client not bootstrapped; cannot drain pool %s", pool)
	}
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "agentpool=" + pool,
	})
	if err != nil {
		return fmt.Errorf("list nodes for pool %s: %w", pool, err)
	}
	if len(nodes.Items) == 0 {
		logf("no nodes to drain in pool %s", pool)
		return nil
	}

	var out, errOut bytes.Buffer
	drainer := &drain.Helper{
		Ctx:                 ctx,
		Client:              c.kube,
		Force:               true,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Timeout:             timeout,
		Out:                 &out,
		ErrOut:              &errOut,
	}
	var evictErrs []error
	for _, n := range nodes.Items {
		name := n.Name
		logf(">>> cordoning %s", name)
		if err := drain.RunCordonOrUncordon(drainer, n.DeepCopy(), true); err != nil {
			return fmt.Errorf("cordon %s: %w", name, err)
		}
		logf(">>> draining %s (timeout=%s)", name, timeout)
		podList, errs := drainer.GetPodsForDeletion(name)
		for _, e := range errs {
			logf("WARN: inspect pods for %s: %v (continuing)", name, e)
		}
		if podList == nil {
			continue
		}
		if warnings := podList.Warnings(); warnings != "" {
			logf("WARN: drain warnings for %s: %s", name, warnings)
		}
		if err := drainer.DeleteOrEvictPods(podList.Pods()); err != nil {
			logf("WARN: evict pods for %s returned: %v (continuing to next node; caller decides if this is fatal)", name, err)
			evictErrs = append(evictErrs, fmt.Errorf("node %s: %w", name, err))
		}
	}
	if out.Len() > 0 {
		logf("drain stdout: %s", out.String())
	}
	if errOut.Len() > 0 {
		logf("drain stderr: %s", errOut.String())
	}
	if len(evictErrs) > 0 {
		return fmt.Errorf("eviction failed on %d node(s) in pool %s: %w", len(evictErrs), pool, errors.Join(evictErrs...))
	}
	return nil
}

func (c *clients) waitForReadyNodes(ctx context.Context, pool string, want int, timeout time.Duration) error {
	if c.kube == nil {
		return fmt.Errorf("kube client not bootstrapped; cannot wait for pool %s", pool)
	}
	deadline := time.Now().Add(timeout)
	var lastListErr error
	for {
		nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: "agentpool=" + pool,
		})
		if err != nil {
			lastListErr = fmt.Errorf("list nodes for pool %s: %w", pool, err)
			logf("WARN: %v; retrying", lastListErr)
		} else {
			lastListErr = nil
			ready := 0
			for i := range nodes.Items {
				if isNodeSchedulableReady(&nodes.Items[i]) {
					ready++
				}
			}
			logf("pool=%s ready=%d/%d", pool, ready, want)
			if ready >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if lastListErr != nil {
				return fmt.Errorf("pool %s did not reach %d ready nodes within %s; last list error: %w", pool, want, timeout, lastListErr)
			}
			return fmt.Errorf("pool %s did not reach %d ready nodes within %s", pool, want, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollIntervalSec * time.Second):
		}
	}
}

func isNodeReady(n *corev1.Node) bool {
	if n == nil {
		return false
	}
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isNodeSchedulableReady(n *corev1.Node) bool {
	if !isNodeReady(n) {
		return false
	}
	if n.Spec.Unschedulable {
		return false
	}
	return n.DeletionTimestamp == nil
}
