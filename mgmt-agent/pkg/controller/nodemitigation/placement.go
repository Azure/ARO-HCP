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

package nodemitigation

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

type ClusterSnapshot struct {
	Nodes      []*corev1.Node
	Pods       []*corev1.Pod
	Events     []corev1.Event
	Namespaces map[string]*corev1.Namespace
	Faulted    map[string]bool
	// NICs includes allocations whose original pods have already disappeared.
	NICs map[string]map[types.UID]int64
}

func requests(pod *corev1.Pod) corev1.ResourceList {
	result := resourcehelper.PodRequests(pod, resourcehelper.PodResourcesOptions{UseStatusResources: true})
	result[corev1.ResourcePods] = *resource.NewQuantity(1, resource.DecimalSI)
	return result
}

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
		if other.Namespace != pod.Namespace || !eligibleNodes[other.Spec.NodeName] || !selector.Matches(labels.Set(other.Labels)) {
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

// placement reserves a feasible destination for all displaced pods together.
// It is conservative: unsupported constraints hold instead of guessing scheduler
// behavior, and external unscheduled demand also consumes the available headroom.
func placement(snapshot ClusterSnapshot, excluded map[string]bool, moving []*corev1.Pod) error {
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
			if !movingUIDs[pod.UID] {
				placed = append(placed, pod)
			}
			if capacity, exists := free[pod.Spec.NodeName]; exists {
				demand := requests(pod)
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
	for _, pod := range moving {
		if err := supportedPod(pod); err != nil {
			return err
		}
		demand := requests(pod)
		fits := false
		for _, name := range destinations {
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
				fits = true
				break
			}
		}
		if !fits {
			return fmt.Errorf("no feasible replacement capacity for %s/%s", pod.Namespace, pod.Name)
		}
	}
	return nil
}
