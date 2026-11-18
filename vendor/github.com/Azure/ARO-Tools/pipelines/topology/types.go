package topology

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"
)

type Topology struct {
	// Services holds the root nodes for service dependency trees.
	Services []Service `json:"services,omitempty"`

	// Entrypoints selects specific sub-trees that are deployed together.
	Entrypoints []Entrypoint `json:"entrypoints,omitempty"`
}

type CombinedTopology struct {
	Topology

	// serviceGroupToTopologyDir maps service group to the directory of the topology file it came from, for use in resolving pipeline paths.
	serviceGroupToTopologyDir map[string]string
}

// Service describes an individual service in the tree.
type Service struct {
	// ServiceGroup is the identifier for this service.
	ServiceGroup string `json:"serviceGroup"`

	// Purpose records a short human-readable blurb on the purpose of this pipeline.
	Purpose string `json:"purpose"`

	// PipelinePath holds the relative path from this topology record to the pipeline definition.
	PipelinePath string `json:"pipelinePath"`

	// Children holds any dependent services.
	Children []Service `json:"children,omitempty"`

	// Metadata is an extension point to store useful information for the service.
	Metadata map[string]string `json:"metadata,omitempty"`

	// Parent service located in a different topology file. Ignored if only one topology file is specified.
	// Only allowed in input yaml on top-level services (i.e. those listed directly under Services in the topology file, not nested within another service's Children).
	ExternalParent *string `json:"externalParent,omitempty"`

	// Stamped marks this service group as stamp-scoped. Children inherit stamped from their parent.
	Stamped *bool `json:"stamped,omitempty"`
}

// Entrypoint describes an individual pipeline in the tree.
type Entrypoint struct {
	// Identifier is the root of the sub-tree selected by this entrypoint.
	Identifier string `json:"identifier"`

	// SafeFly opts this sub-tree into generated SafeFly request submission.
	SafeFly bool `json:"safeFly,omitempty"`

	// Metadata is an extension point to store useful information for the sub-tree.
	Metadata map[string]string `json:"metadata,omitempty"`
}

func Load(path string) (*Topology, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var out Topology
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out.PropagateStamped()
	return &out, nil
}

func LoadCombined(paths []string) (*CombinedTopology, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one topology file must be provided")
	}

	combinedTopology := CombinedTopology{
		serviceGroupToTopologyDir: make(map[string]string),
	}

	servicesWithExternalParents := make([]*Service, 0)
	for _, path := range paths {
		topo, err := Load(path)
		if err != nil {
			return nil, fmt.Errorf("failed to load topology %s: %w", path, err)
		}

		for i := range topo.Services {
			err = validateNoExternalParentInChildren(&topo.Services[i])
			if err != nil {
				return nil, fmt.Errorf("failed to validate topology from %s: %w", path, err)
			}

			err = accumulateServiceGroupToTopologyDirMapping(&topo.Services[i], filepath.Dir(path), combinedTopology.serviceGroupToTopologyDir)
			if err != nil {
				return nil, fmt.Errorf("failed to add topology %s: %w", path, err)
			}

			// If the length of paths is 1, we can ignore external parents since there is only one topology.
			// If the length of paths is greater than 1, we defer adding services with external parents
			// until we've processed all topologies since their external parents may be in later topologies in the list.
			if topo.Services[i].ExternalParent != nil && len(paths) > 1 {
				servicesWithExternalParents = append(servicesWithExternalParents, &topo.Services[i])
			} else {
				combinedTopology.Services = append(combinedTopology.Services, topo.Services[i])
			}
		}
		combinedTopology.Entrypoints = append(combinedTopology.Entrypoints, topo.Entrypoints...)
	}

	for i := range servicesWithExternalParents {
		svc := servicesWithExternalParents[i]
		externalParent, err := combinedTopology.Lookup(*svc.ExternalParent)
		if err != nil {
			// If we can't find the external parent in the combined topology, it's possible it is later
			// in the list of services with external parents since we haven't guaranteed any ordering on the input topologies.
			// Look through the remaining services with external parents to see if we can find it before giving up.
			for j := i + 1; j < len(servicesWithExternalParents); j++ {
				uncheckedSvc := servicesWithExternalParents[j]
				externalParent = find(uncheckedSvc, *svc.ExternalParent)
				if externalParent != nil {
					break
				}
			}

			if externalParent == nil {
				return nil, fmt.Errorf("service %s references external parent %s that does not exist in the topology: %w", svc.ServiceGroup, *svc.ExternalParent, err)
			}
		}
		externalParent.Children = append(externalParent.Children, *svc)
	}

	combinedTopology.PropagateStamped()

	return &combinedTopology, nil
}

// PropagateStamped walks the service tree and sets Stamped = true on all children of stamped parents.
func (t *Topology) PropagateStamped() {
	for i := range t.Services {
		propagateStamped(&t.Services[i], false)
	}
}

func propagateStamped(s *Service, parentStamped bool) {
	if parentStamped && s.Stamped == nil {
		s.Stamped = ptr(true)
	}
	for i := range s.Children {
		propagateStamped(&s.Children[i], s.IsStamped())
	}
}

func (s *Service) IsStamped() bool {
	return s.Stamped != nil && *s.Stamped
}

func ptr[T any](v T) *T {
	return &v
}

func (t *Topology) Validate() error {
	return (&validator{
		seen:       sets.New[string](),
		duplicates: sets.New[string](),
	}).validate(t)
}

func (t *CombinedTopology) GetTopologyDirForServiceGroup(serviceGroup string) (string, error) {
	dir, exists := t.serviceGroupToTopologyDir[serviceGroup]
	if !exists {
		return "", fmt.Errorf("service group %s not found in topology", serviceGroup)
	}
	return dir, nil
}

func accumulateServiceGroupToTopologyDirMapping(s *Service, topologyDir string, mapping map[string]string) error {
	if existingDir, exists := mapping[s.ServiceGroup]; exists {
		return fmt.Errorf("duplicate service group %s found in topology in %s and %s; service groups must be unique across topologies", s.ServiceGroup, topologyDir, existingDir)
	}
	mapping[s.ServiceGroup] = topologyDir
	for i := range s.Children {
		if err := accumulateServiceGroupToTopologyDirMapping(&s.Children[i], topologyDir, mapping); err != nil {
			return err
		}
	}
	return nil
}

func validateNoExternalParentInChildren(s *Service) error {
	for i := range s.Children {
		if s.Children[i].ExternalParent != nil {
			return fmt.Errorf("service %s has externalParent set but is a child of %s; externalParent is only allowed on top-level services", s.Children[i].ServiceGroup, s.ServiceGroup)
		}
		if err := validateNoExternalParentInChildren(&s.Children[i]); err != nil {
			return err
		}
	}
	return nil
}

type validator struct {
	seen       sets.Set[string]
	duplicates sets.Set[string]
}

func (v *validator) validate(t *Topology) error {
	for _, root := range t.Services {
		if err := v.walk(root, false); err != nil {
			return err
		}
	}

	var messages []string
	if v.duplicates.Len() > 0 {
		messages = append(messages, fmt.Sprintf("the following pipelines had duplicate entries: %v", sets.List(v.duplicates)))
	}
	for _, entrypoint := range t.Entrypoints {
		if entrypoint.Identifier == "" {
			messages = append(messages, "entrypoint identifier cannot be empty")
		}
		if !v.seen.Has(entrypoint.Identifier) {
			messages = append(messages, fmt.Sprintf("entrypoint %s was not found in the dependency tree", entrypoint.Identifier))
		}
	}
	if len(messages) > 0 {
		return fmt.Errorf("dependency tree invalid: %s", strings.Join(messages, ", "))
	}
	return nil
}

type InvalidServiceGroupError struct {
	ServiceGroup string
}

func (e *InvalidServiceGroupError) Error() string {
	return fmt.Sprintf("invalid service group %s, must be of form Microsoft.Azure.ARO.Service.Component(.Subcomponent)?", e.ServiceGroup)
}

func (v *validator) walk(s Service, parentStamped bool) error {
	if !strings.HasPrefix(s.ServiceGroup, "Microsoft.Azure.ARO.") {
		return &InvalidServiceGroupError{ServiceGroup: s.ServiceGroup}
	}

	parts := strings.Split(s.ServiceGroup, ".")
	if len(parts) < 4 || len(parts) > 6 {
		return &InvalidServiceGroupError{ServiceGroup: s.ServiceGroup}
	}

	if parts[3] == "" {
		return &InvalidServiceGroupError{ServiceGroup: s.ServiceGroup}
	}

	if parentStamped && s.Stamped != nil && !*s.Stamped {
		return fmt.Errorf("service %s explicitly sets stamped=false but its parent is stamped; stamped subtrees cannot have opt-outs", s.ServiceGroup)
	}

	if v.seen.Has(s.ServiceGroup) {
		v.duplicates.Insert(s.ServiceGroup)
	}
	v.seen.Insert(s.ServiceGroup)

	if err := defaultUsingMetadata(&s.Purpose, s.Metadata, "purpose"); err != nil {
		return fmt.Errorf("failed to default purpose: %w", err)
	}
	if err := defaultUsingMetadata(&s.PipelinePath, s.Metadata, "pipeline"); err != nil {
		return fmt.Errorf("failed to default pipeline: %w", err)
	}

	// parentStamped carries inherited state through intermediates with nil Stamped
	effectiveStamped := parentStamped || s.IsStamped()
	for _, child := range s.Children {
		if err := v.walk(child, effectiveStamped); err != nil {
			return err
		}
	}
	return nil
}

func defaultUsingMetadata(into *string, from map[string]string, key string) error {
	if into == nil {
		panic("programmer error: passed nil 'into' to defaultUsingMetadata")
	}
	if *into != "" {
		// no defaulting needed
		return nil
	}
	if from == nil {
		return fmt.Errorf("field unset and no metadata present, can't default using %q", key)
	}
	value, ok := from[key]
	if !ok || value == "" {
		return fmt.Errorf("field unset and metadata key %q missing or empty", key)
	}
	*into = value
	return nil
}

// ServiceNotFoundError denotes a failure in Lookup() when the requested service group is not in the tree.
type ServiceNotFoundError struct {
	ServiceGroup string
}

func (e *ServiceNotFoundError) Error() string {
	return fmt.Sprintf("service group %s not found in service tree", e.ServiceGroup)
}

// Lookup determines if the serviceGroup in question is part of the service topology tree and returns a pointer to the root
// node, if so.
func (t *Topology) Lookup(serviceGroup string) (*Service, error) {
	// walk the dependency tree from the roots; if we find the serviceGroup we know it's part of build-out
	var service *Service
	for i := range t.Services {
		if found := find(&t.Services[i], serviceGroup); found != nil {
			service = found
			break
		}
	}
	if service == nil {
		return nil, &ServiceNotFoundError{ServiceGroup: serviceGroup}
	}
	return service, nil
}

func find(root *Service, identifier string) *Service {
	if root.ServiceGroup == identifier {
		return root
	}
	for i := range root.Children {
		if found := find(&root.Children[i], identifier); found != nil {
			return found
		}
	}
	return nil
}
