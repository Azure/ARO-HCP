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

package swiftpod

import (
	"fmt"
	"slices"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	resourcehelper "k8s.io/component-helpers/resource"
	"k8s.io/component-helpers/scheduling/corev1/nodeaffinity"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

// Checks replacement placement with the admitted Pod, excluding faults other than its own Pod-scoped stall.
func (c *Controller) checkPlacement(cfg Config, snapshot ClusterSnapshot, pod *corev1.Pod) error {
	if err := snapshot.checkFreshness(c.clock(), cfg.ObservationMaxAge.Duration); err != nil {
		return err
	}
	for _, key := range []string{corev1.LabelTopologyRegion, corev1.LabelTopologyZone} {
		if value, exists := pod.Labels[key]; exists {
			source := slices.IndexFunc(snapshot.Nodes, func(n *corev1.Node) bool { return n.Name == pod.Spec.NodeName })
			if source < 0 {
				return fmt.Errorf("source Node observation missing")
			}
			nodeValue, present := snapshot.Nodes[source].Labels[key]
			if !present || nodeValue != value {
				return fmt.Errorf("pod topology label %s does not match the source Node", key)
			}
		}
	}
	// Bind-time labels are absent during scheduling and can change at the
	// destination. Such selector dependencies need a separate placement policy.
	for _, observed := range append(slices.Clone(snapshot.Pods), pod) {
		if topologyLabelScheduling(observed) {
			return fmt.Errorf("pod topology label selectors require a workload-specific placement policy")
		}
	}
	snapshot.Pods = slices.Clone(snapshot.Pods)
	index := slices.IndexFunc(snapshot.Pods, func(p *corev1.Pod) bool { return p.UID == pod.UID })
	if index < 0 {
		return fmt.Errorf("admitted Pod is missing from the cluster snapshot")
	}
	snapshot.Pods[index] = pod
	faulted := make(map[string]bool, len(snapshot.Faulted))
	for name, value := range snapshot.Faulted {
		faulted[name] = value
	}
	for node, stalledPods := range snapshot.Stalled {
		faulted[node] = faulted[node] || slices.ContainsFunc(stalledPods, func(uid types.UID) bool {
			return uid != pod.UID
		})
	}
	snapshot.Faulted = faulted
	replacement := pod.DeepCopy()
	delete(replacement.Labels, ownershipLabel)
	return placement(snapshot, nil, []*corev1.Pod{replacement})
}

// Detects hard scheduling selectors that depend on bind-time region or zone Pod labels.
func topologyLabelScheduling(pod *corev1.Pod) bool {
	topologyKey := func(key string) bool {
		return key == corev1.LabelTopologyRegion || key == corev1.LabelTopologyZone
	}
	selectorUsesTopology := func(selector *metav1.LabelSelector, matching, mismatching []string) bool {
		if slices.ContainsFunc(matching, topologyKey) || slices.ContainsFunc(mismatching, topologyKey) {
			return true
		}
		if selector != nil {
			for key := range selector.MatchLabels {
				if topologyKey(key) {
					return true
				}
			}
			for _, expression := range selector.MatchExpressions {
				if topologyKey(expression.Key) {
					return true
				}
			}
		}
		return false
	}
	if affinity := pod.Spec.Affinity; affinity != nil {
		var terms []corev1.PodAffinityTerm
		if affinity.PodAffinity != nil {
			terms = append(terms, affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution...)
		}
		if affinity.PodAntiAffinity != nil {
			terms = append(terms, affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution...)
		}
		for _, term := range terms {
			if selectorUsesTopology(term.LabelSelector, term.MatchLabelKeys, term.MismatchLabelKeys) {
				return true
			}
		}
	}
	for _, constraint := range pod.Spec.TopologySpreadConstraints {
		if constraint.WhenUnsatisfiable == corev1.DoNotSchedule &&
			selectorUsesTopology(constraint.LabelSelector, constraint.MatchLabelKeys, nil) {
			return true
		}
	}
	return false
}

// Computes resource demand, including one Pod slot.
func requests(pod *corev1.Pod, options resourcehelper.PodResourcesOptions) corev1.ResourceList {
	result := resourcehelper.PodRequests(pod, options)
	result[corev1.ResourcePods] = *resource.NewQuantity(1, resource.DecimalSI)
	return result
}

// Adds or subtracts resource quantities in place.
func addResources(dst, src corev1.ResourceList, subtract bool) {
	for name, amount := range src {
		value := dst[name]
		if subtract {
			value.Sub(amount)
		} else {
			value.Add(amount)
		}
		dst[name] = value
	}
}

// Checks hard Node taints, requiring indefinite tolerance of NoExecute taints.
func tolerates(pod *corev1.Pod, node *corev1.Node) bool {
	for i := range node.Spec.Taints {
		taint := &node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		found := false
		for _, tolerance := range pod.Spec.Tolerations {
			if tolerance.ToleratesTaint(klog.Background(), taint, false) && (taint.Effect != corev1.TaintEffectNoExecute || tolerance.TolerationSeconds == nil) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Checks whether another Pod matches an affinity term's labels and namespace scope.
func matchesTerm(subject, other *corev1.Pod, term corev1.PodAffinityTerm, namespaces map[string]*corev1.Namespace) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
	if err != nil {
		return false, err
	}
	selector, err = keyedSelector(selector, subject, term.MatchLabelKeys, term.MismatchLabelKeys)
	if err != nil {
		return false, err
	}
	if !selector.Matches(labels.Set(other.Labels)) {
		return false, nil
	}
	if slices.Contains(term.Namespaces, other.Namespace) {
		return true, nil
	}
	if term.NamespaceSelector == nil {
		return len(term.Namespaces) == 0 && subject.Namespace == other.Namespace, nil
	}
	namespace, exists := namespaces[other.Namespace]
	if !exists {
		return false, fmt.Errorf("namespace observation missing")
	}
	return selected(*term.NamespaceSelector, namespace.Labels), nil
}

// Checks required affinity, both Pods' anti-affinity rules, hard topology spread and taints.
func affinityFits(pod *corev1.Pod, node *corev1.Node, placed []*corev1.Pod, nodes map[string]*corev1.Node, namespaces map[string]*corev1.Namespace) (bool, error) {
	if ok, err := nodeaffinity.GetRequiredNodeAffinity(pod).Match(node); err != nil || !ok {
		return ok, err
	}
	check := func(subject *corev1.Pod, term corev1.PodAffinityTerm, wantMatch bool) (bool, error) {
		domain, exists := node.Labels[term.TopologyKey]
		if !exists {
			return false, nil
		}
		for _, other := range placed {
			otherNode := nodes[other.Spec.NodeName]
			if otherNode == nil || otherNode.Labels[term.TopologyKey] != domain {
				continue
			}
			matches, err := matchesTerm(subject, other, term, namespaces)
			if err != nil {
				return false, err
			}
			if matches {
				return wantMatch, nil
			}
		}
		return !wantMatch, nil
	}
	if affinity := pod.Spec.Affinity; affinity != nil {
		if affinity.PodAffinity != nil {
			for _, term := range affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
				if ok, err := check(pod, term, true); err != nil || !ok {
					return ok, err
				}
			}
		}
		if affinity.PodAntiAffinity != nil {
			for _, term := range affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
				if ok, err := check(pod, term, false); err != nil || !ok {
					return ok, err
				}
			}
		}
	}
	for _, other := range placed {
		otherNode := nodes[other.Spec.NodeName]
		if otherNode == nil || other.Spec.Affinity == nil || other.Spec.Affinity.PodAntiAffinity == nil {
			continue
		}
		for _, term := range other.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution {
			matches, err := matchesTerm(other, pod, term, namespaces)
			if err != nil {
				return false, err
			}
			domain, exists := otherNode.Labels[term.TopologyKey]
			if matches && exists && domain == node.Labels[term.TopologyKey] {
				return false, nil
			}
		}
	}
	for _, constraint := range pod.Spec.TopologySpreadConstraints {
		if constraint.WhenUnsatisfiable == corev1.DoNotSchedule {
			if ok, err := spreadFits(pod, node, constraint, placed, nodes); err != nil || !ok {
				return ok, err
			}
		}
	}
	return tolerates(pod, node), nil
}

// Extends a selector with matchLabelKeys and mismatchLabelKeys values from the Pod.
func keyedSelector(selector labels.Selector, pod *corev1.Pod, matching, mismatching []string) (labels.Selector, error) {
	for _, group := range []struct {
		keys     []string
		operator selection.Operator
	}{
		{matching, selection.In}, {mismatching, selection.NotIn},
	} {
		for _, key := range group.keys {
			value, exists := pod.Labels[key]
			if !exists {
				continue
			}
			requirement, err := labels.NewRequirement(key, group.operator, []string{value})
			if err != nil {
				return nil, err
			}
			selector = selector.Add(*requirement)
		}
	}
	return selector, nil
}

// Checks whether placement respects topology skew across eligible domains.
func spreadFits(pod *corev1.Pod, node *corev1.Node, constraint corev1.TopologySpreadConstraint, placed []*corev1.Pod, nodes map[string]*corev1.Node) (bool, error) {
	domain, exists := node.Labels[constraint.TopologyKey]
	if !exists {
		return false, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(constraint.LabelSelector)
	if err != nil {
		return false, err
	}
	selector, err = keyedSelector(selector, pod, constraint.MatchLabelKeys, nil)
	if err != nil {
		return false, err
	}
	counts := map[string]int32{}
	eligibleNodes := map[string]bool{}
	for name, candidate := range nodes {
		value, exists := candidate.Labels[constraint.TopologyKey]
		if !exists {
			continue
		}
		if constraint.NodeAffinityPolicy == nil || *constraint.NodeAffinityPolicy == corev1.NodeInclusionPolicyHonor {
			matches, err := nodeaffinity.GetRequiredNodeAffinity(pod).Match(candidate)
			if err != nil {
				return false, err
			}
			if !matches {
				continue
			}
		}
		if constraint.NodeTaintsPolicy != nil && *constraint.NodeTaintsPolicy == corev1.NodeInclusionPolicyHonor && !tolerates(pod, candidate) {
			continue
		}
		eligibleNodes[name] = true
		counts[value] = 0
	}
	if !eligibleNodes[node.Name] {
		return false, nil
	}
	for _, other := range placed {
		if other.DeletionTimestamp != nil || other.Namespace != pod.Namespace ||
			!eligibleNodes[other.Spec.NodeName] || !selector.Matches(labels.Set(other.Labels)) {
			continue
		}
		counts[nodes[other.Spec.NodeName].Labels[constraint.TopologyKey]]++
	}
	minimum := int32(0)
	minDomains := int32(1)
	if constraint.MinDomains != nil {
		minDomains = *constraint.MinDomains
	}
	if int32(len(counts)) >= minDomains {
		minimum = counts[domain]
		for _, count := range counts {
			minimum = min(minimum, count)
		}
	}
	increment := int32(0)
	if selector.Matches(labels.Set(pod.Labels)) {
		increment = 1
	}
	return counts[domain]+increment-minimum <= constraint.MaxSkew, nil
}

const maxPlacementAttempts = 4096

// Simulates replacement and pending Pod placement with a bounded backtracking search.
// It is conservative: unsupported constraints hold instead of guessing scheduler
// behavior, and external unscheduled demand also consumes the available headroom.
func placement(snapshot ClusterSnapshot, excluded map[string]bool, moving []*corev1.Pod) error {
	moving = slices.Clone(moving)
	nodes := map[string]*corev1.Node{}
	free := map[string]corev1.ResourceList{}
	chargedNICs := map[string]map[types.UID]int64{}
	movingUIDs := map[types.UID]bool{}
	for _, pod := range moving {
		movingUIDs[pod.UID] = true
	}
	placed := make([]*corev1.Pod, 0, len(snapshot.Pods))
	for _, node := range snapshot.Nodes {
		nodes[node.Name] = node
		if ready(node) && !node.Spec.Unschedulable && !excluded[node.Name] && !snapshot.Faulted[node.Name] {
			free[node.Name] = node.Status.Allocatable.DeepCopy()
		}
	}
	for _, pod := range snapshot.Pods {
		if terminal(pod) {
			continue
		}
		if pod.Spec.NodeName != "" {
			if movingUIDs[pod.UID] {
				continue
			}
			placed = append(placed, pod)
			if capacity, exists := free[pod.Spec.NodeName]; exists {
				demand := requests(pod, resourcehelper.PodResourcesOptions{UseStatusResources: true})
				addResources(capacity, demand, true)
				if chargedNICs[pod.Spec.NodeName] == nil {
					chargedNICs[pod.Spec.NodeName] = map[types.UID]int64{}
				}
				nics := demand[kuberesources.SwiftNICResourceName]
				chargedNICs[pod.Spec.NodeName][pod.UID] = nics.Value()
			}
		} else if pod.DeletionTimestamp == nil {
			moving = append(moving, pod)
		}
	}
	for node, allocations := range snapshot.NICs {
		if capacity, exists := free[node]; exists {
			for uid, count := range allocations {
				if extra := count - chargedNICs[node][uid]; extra > 0 {
					addResources(capacity, corev1.ResourceList{
						kuberesources.SwiftNICResourceName: *resource.NewQuantity(extra, resource.DecimalSI),
					}, true)
				}
			}
		}
	}
	sort.Slice(moving, func(i, j int) bool { return string(moving[i].UID) < string(moving[j].UID) })
	destinations := make([]string, 0, len(free))
	for name := range free {
		destinations = append(destinations, name)
	}
	sort.Strings(destinations)
	demands := make([]corev1.ResourceList, len(moving))
	for i, pod := range moving {
		if err := supportedPod(pod); err != nil {
			return err
		}
		// Replacement and pending Pods need their spec requests, even when a resident resize is infeasible.
		demands[i] = requests(pod, resourcehelper.PodResourcesOptions{})
	}
	nextDestination := make([]int, len(moving))
	assignments := make([]string, len(moving))
	attempts := 0
	for index := 0; index < len(moving); {
		pod, demand := moving[index], demands[index]
		fits := false
		for nextDestination[index] < len(destinations) {
			if attempts == maxPlacementAttempts {
				return fmt.Errorf("placement search exhausted its %d pod/node attempts; feasibility is unknown", maxPlacementAttempts)
			}
			attempts++
			name := destinations[nextDestination[index]]
			nextDestination[index]++
			node := nodes[name]
			possible, err := affinityFits(pod, node, placed, nodes, snapshot.Namespaces)
			if err != nil {
				return err
			}
			if !possible {
				continue
			}
			enough := true
			for resourceName, amount := range demand {
				available := free[name][resourceName]
				if available.Cmp(amount) < 0 {
					enough = false
					break
				}
			}
			if enough {
				addResources(free[name], demand, true)
				assigned := pod.DeepCopy()
				assigned.Spec.NodeName = name
				placed = append(placed, assigned)
				assignments[index] = name
				fits = true
				break
			}
		}
		if fits {
			index++
			continue
		}
		if index == 0 {
			return fmt.Errorf("no feasible replacement capacity found for the ordered pod set")
		}
		nextDestination[index] = 0
		index--
		// Undo the simulated assignment before trying its next destination.
		addResources(free[assignments[index]], demands[index], false)
		placed = placed[:len(placed)-1]
	}
	return nil
}
