package graph

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"
)

// Stamp identifies a stamp within a multi-stamp execution graph. Use NewStamp to create a set
// stamp value and Unstamped for unstamped contexts. Callers should use IsSet rather than comparing
// against the zero value directly.
type Stamp struct {
	value string
}

// Unstamped returns the zero value representing an unstamped context.
func Unstamped() Stamp { return Stamp{} }

// NewStamp creates a Stamp from a non-empty string. It returns an error if the value is empty.
func NewStamp(v string) (Stamp, error) {
	if len(v) == 0 {
		return Stamp{}, fmt.Errorf("stamp value must not be empty")
	}
	return Stamp{value: v}, nil
}

// IsSet reports whether the stamp represents a real stamp value rather than the unstamped zero value.
func (s Stamp) IsSet() bool { return len(s.value) > 0 }

func (s Stamp) String() string { return s.value }

// If returns this stamp when stamped is true, otherwise Unstamped().
func (s Stamp) If(stamped bool) Stamp {
	if stamped {
		return s
	}
	return Unstamped()
}

// Identifier records a dependency on a step in a particular service group and resource group.
// This is the minimum amount of precision required to identify a step in a multi-pipeline execution environment.
// Stamp is set by callers that expand stamped services into multiple graph copies — it distinguishes
// nodes that share the same ServiceGroup/ResourceGroup/Step but belong to different stamps.
type Identifier struct {
	Stamp        Stamp
	ServiceGroup string
	types.StepDependency
}

func (i Identifier) ResourceGroupKey() ResourceGroupKey {
	return ResourceGroupKey{Stamp: i.Stamp, Name: i.ResourceGroup}
}

func (i Identifier) String() string {
	if i.Stamp.IsSet() {
		return fmt.Sprintf("%s/%s/%s (stamp=%s)", i.ServiceGroup, i.ResourceGroup, i.Step, i.Stamp)
	}
	return fmt.Sprintf("%s/%s/%s", i.ServiceGroup, i.ResourceGroup, i.Step)
}

// Node records a step along with references to all parents and children. This structure is intentionally devoid of
// complex data, pointers to the underlying structures needed to execute the steps, etc. Such a structure helps to
// make operations that produce or operate over these nodes easy to test and verify.
type Node struct {
	// This embedded Identifier defines the identifier for this node.
	Identifier

	// Children contains the direct children (not further descendants) of this node.
	Children []Identifier
	// Parents contains the direct parents (not further ancestors) of this node.
	Parents []Identifier
}

// ResourceGroupKey identifies a resource group within a graph. Stamp is Unstamped for
// unstamped resource groups. Callers that expand stamped services set Stamp to
// distinguish per-stamp resource group metadata that shares the same logical Name.
type ResourceGroupKey struct {
	Stamp Stamp
	Name  string
}

func newGraphBuilder() *graphBuilder {
	return &graphBuilder{
		Graph: &Graph{
			Services:               map[string]*topology.Service{},
			ResourceGroups:         map[ResourceGroupKey]*types.ResourceGroupMeta{},
			resourceGroupOwners:    map[ResourceGroupKey]sets.Set[string]{},
			Steps:                  map[Identifier]types.Step{},
			Nodes:                  []Node{},
			ServiceValidationSteps: map[Identifier]types.ValidationStep{},
		},
		nodeIndex:            map[Identifier]int{},
		resourceGroupStamped: map[string]stampedRecord{},
	}
}

// stampedRecord tracks the stamped declaration for a resource group name and the service
// that first declared it, so inconsistencies can be reported with both sides.
type stampedRecord struct {
	stamped      bool
	serviceGroup string
}

// graphBuilder holds transient build-time state (like nodeIndex) that should not
// persist on the final Graph. Call build() to discard the builder and return the Graph.
type graphBuilder struct {
	*Graph
	nodeIndex            map[Identifier]int
	resourceGroupStamped map[string]stampedRecord
}

// Graph holds a set of nodes, recording parent/child relationships for each, along with a set of lookup tables for
// the services, resource groups, steps, etc. that the nodes represent.
type Graph struct {
	// Services is a lookup table of services by name (the service group).
	Services map[string]*topology.Service

	// ResourceGroups is a lookup table of resource groups keyed by stamp and logical name.
	// Unstamped resource groups use Unstamped.
	ResourceGroups map[ResourceGroupKey]*types.ResourceGroupMeta

	// Subscription is an optional set of metadata required for subscription provisioning.
	Subscription *Subscription

	// Steps is a lookup table keyed by Identifier.
	Steps map[Identifier]types.Step

	// Nodes records every step, and the parent/child relationships between them.
	Nodes []Node

	// ServiceValidationSteps record the service validation steps
	ServiceValidationSteps map[Identifier]types.ValidationStep

	// resourceGroupOwners tracks which service groups have registered each resource group (internal book-keeping).
	resourceGroupOwners map[ResourceGroupKey]sets.Set[string]
}

// GetStep returns the step for a node, using the node's Stamp field to select per-stamp steps.
func (c *Graph) GetStep(node Identifier) (types.Step, bool) {
	s, exists := c.Steps[node]
	return s, exists
}

// Subscription holds the metadata required to handle subscription provisioning for an execution graph.
type Subscription struct {
	// ServiceGroup records the service which requested the subscription provisioning. In a multi-service execution graph, there may
	// be many services at play; subscription provisioning requires relative path resolution for role assignment ARM templates, so
	// the path to the service's directory must be known.
	ServiceGroup string

	// ResourceGroup records the semantic identifier for the resource group that requested subscription provisioning. The scope tags
	// used to parameterize the subscription provisioning must be tied to a specific resource group.
	ResourceGroup string

	Config types.SubscriptionProvisioning
}

// edge is a record of an inter-step dependency. This struct is unexported as we only use it during graph construction and do not
// expose edges directly as part of the graph.
type edge struct {
	from, to Identifier
}

// stampIteration pairs a stamp identifier with the pipeline to process for that stamp.
// Unstamped services use a single iteration with Unstamped.
type stampIteration struct {
	stamp    Stamp
	pipeline *types.Pipeline
}

// ForPipeline generates a graph for one pipeline, processing all steps therein to determine dependencies between them.
func ForPipeline(service *topology.Service, pipeline *types.Pipeline) (*Graph, error) {
	withoutChildren := &topology.Service{
		ServiceGroup: service.ServiceGroup,
		Purpose:      service.Purpose,
		PipelinePath: service.PipelinePath,
		Children:     nil, // explicitly omitted to generate graph for one pipeline only
		Metadata:     service.Metadata,
		Stamped:      service.Stamped,
	}

	graphBuilder := newGraphBuilder()

	stampPipelines := map[Stamp]map[string]*types.Pipeline{
		Unstamped(): {pipeline.ServiceGroup: pipeline},
	}
	if err := graphBuilder.accumulate(withoutChildren, stampPipelines); err != nil {
		return nil, err
	}

	return graphBuilder.Graph, graphBuilder.detectCycles()
}

// ForStampedPipeline generates a graph for a single service group, expanding stamped services once per stamp.
// Like ForPipeline it strips children so the graph covers exactly one pipeline, but unlike ForPipeline it
// preserves the service's Stamped flag and accepts per-stamp pipelines. Unstamped services produce a single
// set of nodes with Unstamped; stamped services produce N copies — one per stamp with IsSet() == true.
func ForStampedPipeline(service *topology.Service, stampPipelines map[Stamp]map[string]*types.Pipeline) (*Graph, error) {
	withoutChildren := &topology.Service{
		ServiceGroup: service.ServiceGroup,
		Purpose:      service.Purpose,
		PipelinePath: service.PipelinePath,
		Children:     nil,
		Metadata:     service.Metadata,
		Stamped:      service.Stamped,
	}

	graphBuilder := newGraphBuilder()

	if err := graphBuilder.accumulate(withoutChildren, stampPipelines); err != nil {
		return nil, err
	}

	return graphBuilder.Graph, graphBuilder.detectCycles()
}

// ForEntrypoint generates a graph for all pipelines in the sub-tree of the topology identified by the entrypoint.
// Convenience wrapper around ForEntrypoints for a single entrypoint.
func ForEntrypoint(topo *topology.Topology, entrypoint *topology.Entrypoint, pipelines map[string]*types.Pipeline) (*Graph, error) {
	return ForEntrypoints(topo, []*topology.Entrypoint{entrypoint}, pipelines)
}

// ForEntrypoints generates a graph for all pipelines in the sub-trees of the topology identified by the entrypoints.
// Stamped services in the topology are not expanded — each appears exactly once with Unstamped on its
// nodes. The resulting graph has one set of nodes per service, making it suitable for contexts where stamp expansion
// is handled by the runtime (e.g. EV2 rollout specs) rather than the graph itself.
func ForEntrypoints(topo *topology.Topology, entrypoints []*topology.Entrypoint, pipelines map[string]*types.Pipeline) (*Graph, error) {
	return forEntrypoints(topo, entrypoints, map[Stamp]map[string]*types.Pipeline{Unstamped(): pipelines})
}

// ForStampedEntrypoints generates a graph for all pipelines in the sub-trees of the topology identified by the
// entrypoints, expanding stamped services once per stamp. Each stamped service produces N copies of its nodes —
// one per stamp with IsSet() == true in stampPipelines — with each copy carrying a distinct Stamp on its identifiers.
// Unstamped services appear once with Unstamped. The resulting graph is suitable for contexts where the graph
// itself drives per-stamp execution (e.g. templatize concurrent stamp rollouts).
func ForStampedEntrypoints(topo *topology.Topology, entrypoints []*topology.Entrypoint, stampPipelines map[Stamp]map[string]*types.Pipeline) (*Graph, error) {
	return forEntrypoints(topo, entrypoints, stampPipelines)
}

func forEntrypoints(topo *topology.Topology, entrypoints []*topology.Entrypoint, stampPipelines map[Stamp]map[string]*types.Pipeline) (*Graph, error) {
	var roots []*topology.Service
	for _, entrypoint := range entrypoints {
		root, err := topo.Lookup(entrypoint.Identifier)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup entrypoint %s: %v", entrypoint.Identifier, err)
		}
		roots = append(roots, root)
	}

	graphBuilder := newGraphBuilder()

	for _, root := range roots {
		if err := graphBuilder.accumulate(root, stampPipelines); err != nil {
			return nil, err
		}
	}

	// External step dependencies break the nice separation between nodes of one pipeline and the rest of the graph,
	// so the nodesFor() method can no longer generate bi-directional edges as it does not see other nodes to add
	// child relations. Instead of trying to teach nodesFor() how to do half of these edges, we can do a pass
	// now with full context.
	if err := graphBuilder.addExternalDependencyEdges(); err != nil {
		return nil, err
	}

	return graphBuilder.Graph, graphBuilder.detectCycles()
}

// accumulate recursively traverses the service and all children, building a graph of how steps in each
// service depend on each other. Stamped services are expanded once per stamp; unstamped services once.
func (c *graphBuilder) accumulate(service *topology.Service, stampPipelines map[Stamp]map[string]*types.Pipeline) error {
	if _, alreadyRecorded := c.Services[service.ServiceGroup]; alreadyRecorded {
		return fmt.Errorf("service group %s already recorded", service.ServiceGroup)
	}
	c.Services[service.ServiceGroup] = service

	iterations, err := resolveIterations(service, stampPipelines)
	if err != nil {
		return err
	}

	// Leaf nodes are collected per stamp so that inter-service wiring connects only matching stamps.
	allLeaves := map[Stamp][]Identifier{}
	for _, iter := range iterations {
		leaves, err := c.accumulateIteration(service.ServiceGroup, iter)
		if err != nil {
			return err
		}
		for _, leaf := range leaves {
			allLeaves[leaf.Stamp] = append(allLeaves[leaf.Stamp], leaf)
		}
	}
	for stamp, leaves := range allLeaves {
		allLeaves[stamp] = deduplicateIdentifiers(leaves)
	}

	// Wire inter-service edges: connect this service's leaves to child service roots.
	for _, child := range service.Children {
		if err := c.accumulate(&child, stampPipelines); err != nil {
			return err
		}
		if err := c.wireInterServiceEdges(&child, allLeaves); err != nil {
			return err
		}
	}

	return nil
}

// resolveIterations determines which stamp/pipeline pairs to process for a service.
// Unstamped services run once with Unstamped. Stamped services expand once per
// stamp key where IsSet() is true.
func resolveIterations(service *topology.Service, stampPipelines map[Stamp]map[string]*types.Pipeline) ([]stampIteration, error) {
	// Unstamped services use the same pipeline regardless of stamp — grab from any entry.
	if !service.IsStamped() {
		for _, pipelines := range stampPipelines {
			if p, exists := pipelines[service.ServiceGroup]; exists {
				return []stampIteration{{pipeline: p}}, nil
			}
		}
		return nil, fmt.Errorf("pipeline for service %s not found", service.ServiceGroup)
	}

	var stamps []Stamp
	for stamp := range stampPipelines {
		if stamp.IsSet() {
			stamps = append(stamps, stamp)
		}
	}
	slices.SortFunc(stamps, func(a, b Stamp) int {
		return strings.Compare(a.String(), b.String())
	})

	// No set stamps: fall back to single iteration without expansion.
	if len(stamps) == 0 {
		for _, pipelines := range stampPipelines {
			if p, exists := pipelines[service.ServiceGroup]; exists {
				return []stampIteration{{pipeline: p}}, nil
			}
		}
		return nil, fmt.Errorf("pipeline for service %s not found", service.ServiceGroup)
	}

	var iterations []stampIteration
	for _, stamp := range stamps {
		pipeline, exists := stampPipelines[stamp][service.ServiceGroup]
		if !exists {
			return nil, fmt.Errorf("pipeline for service %s not found in stamp %s", service.ServiceGroup, stamp)
		}
		iterations = append(iterations, stampIteration{stamp: stamp, pipeline: pipeline})
	}
	return iterations, nil
}

// accumulateIteration processes one stamp iteration: generates nodes, registers resource groups,
// steps, and metadata, and returns the leaf nodes for inter-service wiring.
func (c *graphBuilder) accumulateIteration(serviceGroup string, iter stampIteration) ([]Identifier, error) {
	service := c.Services[serviceGroup]

	resourceGroups, subscription, steps, serviceValidationSteps, nodes, err := nodesFor(iter.pipeline, iter.stamp)
	if err != nil {
		return nil, fmt.Errorf("failed to generate graph for pipeline %s: %v", serviceGroup, err)
	}

	if !service.IsStamped() {
		for name, rg := range resourceGroups {
			if rg.Stamped {
				return nil, fmt.Errorf("unstamped service %q defines stamped resource group %q", serviceGroup, name)
			}
		}
	}

	for name, group := range resourceGroups {
		if err := c.registerResourceGroup(serviceGroup, iter.stamp, name, group); err != nil {
			return nil, err
		}
	}

	if subscription != nil && c.Subscription != nil {
		return nil, fmt.Errorf("subscription provisioning already recorded for %s/%s, cannot add another for %s/%s", c.Subscription.ServiceGroup, c.Subscription.ResourceGroup, subscription.ServiceGroup, subscription.ResourceGroup)
	}
	if subscription != nil {
		c.Subscription = subscription
	}

	// Register steps and validation steps with stamp-qualified identifiers.
	// Stamped RGs use the iteration stamp; unstamped RGs use Unstamped().
	for rg, stepMap := range steps {
		for stepName, step := range stepMap {
			rgStamp := iter.stamp.If(resourceGroups[rg].Stamped)
			// Unstamped RGs produce identical keys across stamp iterations; overwrite is safe.
			c.Steps[Identifier{Stamp: rgStamp, ServiceGroup: serviceGroup, StepDependency: types.StepDependency{ResourceGroup: rg, Step: stepName}}] = step
		}
	}
	maps.Copy(c.ServiceValidationSteps, serviceValidationSteps)
	for _, n := range nodes {
		c.registerNode(n)
	}

	return c.findLeaves(nodes)
}

// registerNode adds a node to the graph or merges its edges into an existing node with the same
// Identifier. Merging is needed because unstamped RG nodes within stamped services are produced by
// every stamp iteration with identical Identifiers but different edges (e.g. deploy(stamp=1) vs
// deploy(stamp=2) as children). Sorting and compacting after each merge keeps edges deduplicated.
func (c *graphBuilder) registerNode(n Node) {
	idx, exists := c.nodeIndex[n.Identifier]
	if !exists {
		idx = len(c.Nodes)
		c.nodeIndex[n.Identifier] = idx
		c.Nodes = append(c.Nodes, Node{Identifier: n.Identifier})
	}
	existing := &c.Nodes[idx]
	existing.Children = deduplicateIdentifiers(append(existing.Children, n.Children...))
	existing.Parents = deduplicateIdentifiers(append(existing.Parents, n.Parents...))
}

// registerResourceGroup records a resource group, checking stamped consistency across
// pipelines and detecting metadata conflicts across services.
func (c *graphBuilder) registerResourceGroup(serviceGroup string, stamp Stamp, name string, group *types.ResourceGroupMeta) error {
	if existing, ok := c.resourceGroupStamped[name]; ok && existing.stamped != group.Stamped {
		return fmt.Errorf("resource group %q has inconsistent stamped declaration: stamped=%v in service %q, stamped=%v in service %q", name, existing.stamped, existing.serviceGroup, group.Stamped, serviceGroup)
	}
	c.resourceGroupStamped[name] = stampedRecord{stamped: group.Stamped, serviceGroup: serviceGroup}

	key := ResourceGroupKey{Stamp: stamp.If(group.Stamped), Name: name}

	other, alreadyRecorded := c.ResourceGroups[key]
	if alreadyRecorded && !resourceGroupMetaEqual(group, other) {
		existingOwners := sets.List(c.resourceGroupOwners[key])
		return fmt.Errorf("resource group %s already recorded with different step meta (existing services: %s, new service: %s), diff: %v", name, strings.Join(existingOwners, ", "), serviceGroup, cmp.Diff(group, other))
	}
	if !alreadyRecorded {
		c.ResourceGroups[key] = group
		c.resourceGroupOwners[key] = sets.New[string]()
	}
	c.resourceGroupOwners[key].Insert(serviceGroup)
	return nil
}

// findLeaves identifies leaf nodes for a stamp iteration — these will become parents of child service roots.
// A node is a leaf when it is considered for service group completion and none of its children are.
func (c *graphBuilder) findLeaves(nodes []Node) ([]Identifier, error) {
	var leaves []Identifier
	for _, node := range nodes {
		_, _, step, err := c.lookup(node.Identifier)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup node: %v", err)
		}
		if !step.ConsideredForServiceGroupCompletion() {
			continue
		}
		has, err := c.hasCompletionChild(node)
		if err != nil {
			return nil, err
		}
		if has {
			continue
		}
		leaves = append(leaves, node.Identifier)
	}
	return leaves, nil
}

func (c *graphBuilder) hasCompletionChild(node Node) (bool, error) {
	for _, child := range node.Children {
		_, _, childStep, err := c.lookup(child)
		if err != nil {
			return false, fmt.Errorf("failed to lookup child node %s of %s: %w", child, node.Identifier, err)
		}
		if childStep.ConsideredForServiceGroupCompletion() {
			return true, nil
		}
	}
	return false, nil
}

// wireInterServiceEdges connects parent service leaves to child service roots.
//
// The data we're using to build this graph come in two levels of granularity:
//   - specific, intra-service step relationships defined in a pipeline
//   - granular, inter-service relationships defined in the topology
//
// accumulate() will have built a sub-graph of step nodes for the specific child service,
// which we now need to decorate to record that all steps in that child depend on the parent service.
// There is no defined "end" to a pipeline, nor a "start", as each service may itself be a forest - having
// many roots and many leaves. Therefore, the simplest approach here is to record that every root node
// of the child depends on all the leaf nodes of the parent service, and vice versa.
//
// When stamps are involved, wiring is stamp-scoped: stamp-1 leaves connect to stamp-1 roots only.
// Unstamped leaves (Unstamped) connect to all child roots that share Unstamped or fan out
// to all stamps when the child is stamped and the parent is not.
func (c *graphBuilder) wireInterServiceEdges(child *topology.Service, allLeaves map[Stamp][]Identifier) error {
	for parentStamp, leaves := range allLeaves {
		roots := c.findChildRoots(child, parentStamp)
		rootList := roots.UnsortedList()
		slices.SortFunc(rootList, CompareDependencies)

		for _, root := range rootList {
			idx, err := c.node(root)
			if err != nil {
				return fmt.Errorf("failed to find root node: %v", err)
			}
			c.Nodes[idx].Parents = append(c.Nodes[idx].Parents, leaves...)
		}

		for _, leaf := range leaves {
			idx, err := c.node(leaf)
			if err != nil {
				return fmt.Errorf("failed to find leaf node: %v", err)
			}
			c.Nodes[idx].Children = append(c.Nodes[idx].Children, rootList...)
		}
	}
	return nil
}

// findChildRoots returns identifiers of root nodes for the child service.
// A node is a root of its service when it has no intra-service parents.
// Parents added by inter-service wiring (from other ServiceGroups) do not disqualify it.
// When both parent and child are stamped, only matching-stamp and unstamped-RG roots are returned.
func (c *graphBuilder) findChildRoots(child *topology.Service, parentStamp Stamp) sets.Set[Identifier] {
	roots := sets.New[Identifier]()
	for _, node := range c.Nodes {
		if node.ServiceGroup != child.ServiceGroup || c.hasIntraServiceParent(node) {
			continue
		}
		// Skip child roots whose stamp doesn't match. Unstamped parents or
		// unstamped-RG child roots (IsSet=false) are never filtered — they fan out.
		if parentStamp.IsSet() && node.Stamp.IsSet() && node.Stamp != parentStamp {
			continue
		}
		roots.Insert(node.Identifier)
	}
	return roots
}

func (c *graphBuilder) hasIntraServiceParent(node Node) bool {
	for _, parent := range node.Parents {
		if parent.ServiceGroup == node.ServiceGroup {
			return true
		}
	}
	return false
}

func (c *Graph) lookup(node Identifier) (*topology.Service, *types.ResourceGroupMeta, types.Step, error) {
	svc, exists := c.Services[node.ServiceGroup]
	if !exists {
		return nil, nil, nil, fmt.Errorf("service %s does not exist", node.ServiceGroup)
	}
	resourceGroup, exists := c.ResourceGroups[node.ResourceGroupKey()]
	if !exists {
		return nil, nil, nil, fmt.Errorf("resource group %s for node %s does not exist", node.ResourceGroup, node)
	}
	step, exists := c.GetStep(node)
	if !exists {
		return nil, nil, nil, fmt.Errorf("step %s does not exist", node)
	}
	return svc, resourceGroup, step, nil
}

func (c *graphBuilder) node(id Identifier) (int, error) {
	idx, ok := c.nodeIndex[id]
	if !ok {
		return 0, fmt.Errorf("node %s not found", id)
	}
	return idx, nil
}

func (c *graphBuilder) addExternalDependencyEdges() error {
	for i, node := range c.Nodes {
		step, ok := c.GetStep(node.Identifier)
		if !ok {
			return fmt.Errorf("step %s not found", node.Identifier)
		}
		external := step.ExternalDependencies()
		if len(external) == 0 {
			continue
		}
		for _, dep := range external {
			parent := Identifier{
				ServiceGroup: dep.ServiceGroup,
				StepDependency: types.StepDependency{
					ResourceGroup: dep.ResourceGroup,
					Step:          dep.Step,
				},
			}

			targetRec, ok := c.resourceGroupStamped[dep.ResourceGroup]
			if !ok {
				return fmt.Errorf("step %q in resource group %q (service %q) has external dependency on unknown resource group %q in service %q", node.Step, node.ResourceGroup, node.ServiceGroup, dep.ResourceGroup, dep.ServiceGroup)
			}
			targetRGStamped := targetRec.stamped

			sourceRec, ok := c.resourceGroupStamped[node.ResourceGroup]
			if !ok {
				return fmt.Errorf("step %q references unknown resource group %q (service %q)", node.Step, node.ResourceGroup, node.ServiceGroup)
			}
			sourceRGStamped := sourceRec.stamped
			if targetRGStamped && !sourceRGStamped {
				return fmt.Errorf("step %q in unstamped resource group %q (service %q) has external dependency on stamped resource group %q in service %q", node.Step, node.ResourceGroup, node.ServiceGroup, dep.ResourceGroup, dep.ServiceGroup)
			}

			if targetRGStamped {
				parent.Stamp = node.Stamp
			}
			parentNodeIdx, err := c.node(parent)
			if err != nil {
				return err
			}
			parentNode := c.Nodes[parentNodeIdx]
			parentNode.Children = deduplicateIdentifiers(append(parentNode.Children, node.Identifier))
			c.Nodes[parentNodeIdx] = parentNode

			node.Parents = append(node.Parents, parent)
		}
		node.Parents = deduplicateIdentifiers(node.Parents)
		c.Nodes[i] = node
	}
	return nil
}

// nodesFor transforms a pipeline to the list of nodes and lookup tables required in a graph
func nodesFor(pipeline *types.Pipeline, stamp Stamp) (
	map[string]*types.ResourceGroupMeta,
	*Subscription,
	map[string]map[string]types.Step,
	map[Identifier]types.ValidationStep,
	[]Node,
	error,
) {
	stepsByResourceGroupAndName := map[string]map[string]types.Step{}
	serviceValidationSteps := map[Identifier]types.ValidationStep{}
	resourceGroupsByName := map[string]*types.ResourceGroupMeta{}
	rgStamped := map[string]bool{}
	var subscription *Subscription
	for _, rg := range pipeline.ResourceGroups {
		resourceGroupsByName[rg.Name] = rg.ResourceGroupMeta
		rgStamped[rg.Name] = rg.Stamped
		if rg.SubscriptionProvisioning != nil && subscription != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("multiple subscriptions found for pipeline %s", pipeline.ServiceGroup)
		}
		if rg.SubscriptionProvisioning != nil {
			subscription = &Subscription{
				ServiceGroup:  pipeline.ServiceGroup,
				ResourceGroup: rg.Name,
				Config:        *rg.SubscriptionProvisioning,
			}
		}
		stepsByResourceGroupAndName[rg.Name] = map[string]types.Step{}
		for _, step := range rg.Steps {
			stepsByResourceGroupAndName[rg.Name][step.StepName()] = step
		}
		for _, step := range rg.ValidationSteps {
			serviceValidationSteps[Identifier{
				Stamp:        stamp.If(rg.Stamped),
				ServiceGroup: pipeline.ServiceGroup,
				StepDependency: types.StepDependency{
					ResourceGroup: rg.Name,
					Step:          step.StepName(),
				},
			}] = step
		}
	}

	var stepDependencies []edge
	for _, rg := range pipeline.ResourceGroups {
		for _, step := range rg.Steps {
			dependsOn := append(step.Dependencies(), step.RequiredInputs()...)
			slices.SortFunc(dependsOn, CompareStepDependencies)
			dependsOn = slices.Compact(dependsOn)

			for _, dep := range dependsOn {
				stepDependencies = append(stepDependencies, edge{
					from: Identifier{
						Stamp:        stamp.If(rgStamped[dep.ResourceGroup]),
						ServiceGroup: pipeline.ServiceGroup,
						StepDependency: types.StepDependency{
							ResourceGroup: dep.ResourceGroup,
							Step:          dep.Step,
						},
					},
					to: Identifier{
						Stamp:        stamp.If(rgStamped[rg.Name]),
						ServiceGroup: pipeline.ServiceGroup,
						StepDependency: types.StepDependency{
							ResourceGroup: rg.Name,
							Step:          step.StepName(),
						},
					},
				})
			}
		}
	}

	slices.SortFunc(stepDependencies, func(a, b edge) int {
		if comparison := CompareDependencies(a.from, b.from); comparison != 0 {
			return comparison
		}
		return CompareDependencies(a.to, b.to)
	})

	childrenOf := map[Identifier][]Identifier{}
	parentsOf := map[Identifier][]Identifier{}
	for _, e := range stepDependencies {
		childrenOf[e.from] = append(childrenOf[e.from], e.to)
		parentsOf[e.to] = append(parentsOf[e.to], e.from)
	}

	var nodes []Node
	for resourceGroup, steps := range stepsByResourceGroupAndName {
		for stepName := range steps {
			id := Identifier{
				Stamp:        stamp.If(rgStamped[resourceGroup]),
				ServiceGroup: pipeline.ServiceGroup,
				StepDependency: types.StepDependency{
					ResourceGroup: resourceGroup,
					Step:          stepName,
				},
			}
			nodes = append(nodes, Node{
				Identifier: id,
				Children:   childrenOf[id],
				Parents:    parentsOf[id],
			})
		}
	}

	slices.SortFunc(nodes, func(a, b Node) int {
		return CompareDependencies(a.Identifier, b.Identifier)
	})

	return resourceGroupsByName, subscription, stepsByResourceGroupAndName, serviceValidationSteps, nodes, nil
}

func deduplicateIdentifiers(ids []Identifier) []Identifier {
	slices.SortFunc(ids, CompareDependencies)
	return slices.Compact(ids)
}

func CompareDependencies(a, b Identifier) int {
	if comparison := strings.Compare(a.Stamp.String(), b.Stamp.String()); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(a.ServiceGroup, b.ServiceGroup); comparison != 0 {
		return comparison
	}
	return CompareStepDependencies(a.StepDependency, b.StepDependency)
}

func CompareStepDependencies(a, b types.StepDependency) int {
	if comparison := strings.Compare(a.ResourceGroup, b.ResourceGroup); comparison != 0 {
		return comparison
	}
	return strings.Compare(a.Step, b.Step)
}

func resourceGroupMetaEqual(a, b *types.ResourceGroupMeta) bool {
	if a.Name != b.Name {
		return false
	}
	if a.ResourceGroup != b.ResourceGroup {
		return false
	}
	if a.Subscription != b.Subscription {
		return false
	}
	if a.Stamped != b.Stamped {
		return false
	}
	if len(a.ExecutionConstraints) != len(b.ExecutionConstraints) {
		return false
	}
	for i, ac := range a.ExecutionConstraints {
		bc := b.ExecutionConstraints[i]
		if ac.Singleton != bc.Singleton {
			return false
		}
		if ac.Level != bc.Level {
			return false
		}
		if !sets.New(ac.Clouds...).Equal(sets.New(bc.Clouds...)) {
			return false
		}
		if !sets.New(ac.Environments...).Equal(sets.New(bc.Environments...)) {
			return false
		}
		if !sets.New(ac.Regions...).Equal(sets.New(bc.Regions...)) {
			return false
		}
	}

	return true
}

// detectCycles uses 3-color DFS to detect cycles in O(V+E) time.
func (c *Graph) detectCycles() error {
	nodesByID := make(map[Identifier]Node, len(c.Nodes))
	for _, node := range c.Nodes {
		nodesByID[node.Identifier] = node
	}

	const (
		white uint8 = iota // unvisited
		gray               // on current DFS path
		black              // fully explored
	)
	color := make(map[Identifier]uint8, len(c.Nodes))
	var path []Identifier

	var visit func(id Identifier) error
	visit = func(id Identifier) error {
		switch color[id] {
		case black:
			return nil
		case gray:
			var cycle []string
			for _, p := range path {
				cycle = append(cycle, p.String())
			}
			return fmt.Errorf("cycle detected, reached %s via %s", id, strings.Join(cycle, " -> "))
		}

		color[id] = gray
		path = append(path, id)

		for _, child := range nodesByID[id].Children {
			if _, ok := nodesByID[child]; !ok {
				return fmt.Errorf("could not find child node %s - programmer error", child)
			}
			if err := visit(child); err != nil {
				return err
			}
		}

		path = path[:len(path)-1]
		color[id] = black
		return nil
	}

	for _, node := range c.Nodes {
		if color[node.Identifier] == white {
			if err := visit(node.Identifier); err != nil {
				return err
			}
		}
	}
	return nil
}

const graphPrefix = `digraph regexp { 
 fontname="Helvetica,Arial,sans-serif"
 node [fontname="Helvetica,Arial,sans-serif"]
 edge [fontname="Helvetica,Arial,sans-serif"]
`

const graphSuffix = `}`

// MarshalDOT marshals the graph into the DOT notation used by the graphviz library.
// See documentation here: https://graphviz.gitlab.io/doc/info/lang.html
func MarshalDOT(g *Graph) ([]byte, error) {
	out := bytes.Buffer{}
	if n, err := out.WriteString(graphPrefix); err != nil || n != len(graphPrefix) {
		return nil, fmt.Errorf("failed to write graph prefix: wrote %d/%d bytes: %w", n, len(graphPrefix), err)
	}

	stampColors := buildStampColorMap(g.Nodes)

	for _, node := range g.Nodes {
		serviceGroup, err := shortenServiceGroup(node.ServiceGroup)
		if err != nil {
			return nil, err
		}

		nodeID := dotID(serviceGroup, node.Identifier)
		nodeLabel := dotLabel(serviceGroup, node.Identifier, g.ResourceGroups)
		attrs := fmt.Sprintf("label=\"%s\"", nodeLabel)
		if color, ok := stampColors[node.Stamp]; ok {
			attrs += fmt.Sprintf(" style=filled fillcolor=\"%s\"", color)
		}
		if _, err := fmt.Fprintf(&out, " \"%s\" [%s];\n", nodeID, attrs); err != nil {
			return nil, err
		}

		for _, child := range node.Children {
			childServiceGroup, err := shortenServiceGroup(child.ServiceGroup)
			if err != nil {
				return nil, err
			}

			childID := dotID(childServiceGroup, child)
			if _, err := fmt.Fprintf(&out, " \"%s\" -> \"%s\";\n", nodeID, childID); err != nil {
				return nil, err
			}
		}
	}

	for identifier := range g.ServiceValidationSteps {
		shortServiceGroup, err := shortenServiceGroup(identifier.ServiceGroup)
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(&out, " \"serviceValidation\" -> \"%s\";\n", dotID(shortServiceGroup, identifier)); err != nil {
			return nil, err
		}
	}

	if n, err := out.WriteString(graphSuffix); err != nil || n != len(graphSuffix) {
		return nil, fmt.Errorf("failed to write graph suffix: wrote %d/%d bytes: %w", n, len(graphSuffix), err)
	}
	return out.Bytes(), nil
}

var stampColorPalette = []string{
	"#B3D9FF", // light blue
	"#FFD9B3", // light orange
	"#B3FFB3", // light green
	"#FFB3D9", // light pink
	"#D9B3FF", // light purple
	"#FFFFB3", // light yellow
	"#B3FFFF", // light cyan
	"#FFB3B3", // light red
}

func buildStampColorMap(nodes []Node) map[Stamp]string {
	stamps := sets.New[Stamp]()
	for _, node := range nodes {
		if node.Stamp.IsSet() {
			stamps.Insert(node.Stamp)
		}
	}
	sorted := stamps.UnsortedList()
	slices.SortFunc(sorted, func(a, b Stamp) int {
		return strings.Compare(a.String(), b.String())
	})
	colors := make(map[Stamp]string, len(sorted))
	for i, stamp := range sorted {
		colors[stamp] = stampColorPalette[i%len(stampColorPalette)]
	}
	return colors
}

func shortenServiceGroup(serviceGroup string) (string, error) {
	parts := strings.Split(serviceGroup, ".")
	if len(parts) < 5 {
		return "", fmt.Errorf("invalid service group: %q (expected at least 5 dot-separated parts, e.g. \"a.b.c.d.e\")", serviceGroup)
	}

	return strings.Join(parts[4:], "."), nil
}

func dotID(shortSG string, id Identifier) string {
	if id.Stamp.IsSet() {
		return fmt.Sprintf("%s_%s_%s_%s", shortSG, id.ResourceGroup, id.Step, id.Stamp)
	}
	return fmt.Sprintf("%s_%s_%s", shortSG, id.ResourceGroup, id.Step)
}

func dotLabel(shortSG string, id Identifier, resourceGroups map[ResourceGroupKey]*types.ResourceGroupMeta) string {
	rgName := id.ResourceGroup
	if rg, ok := resourceGroups[id.ResourceGroupKey()]; ok {
		rgName = rg.ResourceGroup
	}
	if id.Stamp.IsSet() {
		return fmt.Sprintf("%s/%s/%s (stamp=%s)", shortSG, rgName, id.Step, id.Stamp)
	}
	return fmt.Sprintf("%s/%s/%s", shortSG, rgName, id.Step)
}
