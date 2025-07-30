package topology

import (
	"fmt"
	"os"
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
}

// Entrypoint describes an individual pipeline in the tree.
type Entrypoint struct {
	// Identifier is the root of the sub-tree selected by this entrypoint.
	Identifier string `json:"identifier"`

	// Metadata is an extension point to store useful information for the sub-tree.
	Metadata map[string]string `json:"metadata,omitempty"`
}

func Load(path string) (*Topology, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var out Topology
	return &out, yaml.Unmarshal(raw, &out)
}

func (t *Topology) Validate() error {
	return (&validator{
		seen:       sets.New[string](),
		duplicates: sets.New[string](),
	}).validate(t)
}

type validator struct {
	seen       sets.Set[string]
	duplicates sets.Set[string]
}

func (v *validator) validate(t *Topology) error {
	for _, root := range t.Services {
		if err := v.walk(root); err != nil {
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
	return fmt.Sprintf("invalid service group %s, must be of form Microsoft.Azure.ARO.{Classic|HCP}.Component(.Subcomponent)?", e.ServiceGroup)
}

func (v *validator) walk(s Service) error {
	if !strings.HasPrefix(s.ServiceGroup, "Microsoft.Azure.ARO.") {
		return &InvalidServiceGroupError{ServiceGroup: s.ServiceGroup}
	}

	parts := strings.Split(s.ServiceGroup, ".")
	if len(parts) < 4 || len(parts) > 6 {
		return &InvalidServiceGroupError{ServiceGroup: s.ServiceGroup}
	}

	if parts[3] != "Classic" && parts[3] != "HCP" {
		return &InvalidServiceGroupError{ServiceGroup: s.ServiceGroup}
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

	for _, child := range s.Children {
		if err := v.walk(child); err != nil {
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
