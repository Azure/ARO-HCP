package types

import (
	"fmt"
)

type Components map[string]string

// Release is the first-class identifier for a release
type ReleaseId struct {
	SourceRevision   string `json:"sourceRevision" yaml:"sourceRevision"`
	PipelineRevision string `json:"pipelineRevision" yaml:"pipelineRevision"`
}

func NewReleaseId(sourceRevision, pipelineRevision string) *ReleaseId {
	return &ReleaseId{
		SourceRevision:   sourceRevision,
		PipelineRevision: pipelineRevision,
	}
}

func (r ReleaseId) String() string {
	return fmt.Sprintf("%s-%s", r.SourceRevision, r.PipelineRevision)
}

// ReleaseMetadata describes how and when a release was created
type ReleaseMetadata struct {
	ReleaseId        ReleaseId `json:"releaseId" yaml:"-"`
	Branch           string    `json:"branch" yaml:"branch"`
	Timestamp        string    `json:"timestamp" yaml:"timestamp"`
	PullRequestID    int       `json:"pullRequestId" yaml:"pullRequestId"`
	ServiceGroup     string    `json:"serviceGroup" yaml:"serviceGroup"`
	ServiceGroupBase string    `json:"serviceGroupBase" yaml:"serviceGroupBase"`
}

// DeploymentTarget describes where a release is being deployed
type DeploymentTarget struct {
	Cloud         string   `json:"cloud" yaml:"cloud"`
	Environment   string   `json:"environment" yaml:"environment"`
	RegionConfigs []string `json:"regionConfigs" yaml:"regionConfigs"`
}

// ReleaseDeployment represents deploying a Release to a specific Target
type ReleaseDeployment struct {
	Metadata   ReleaseMetadata   `json:"metadata" yaml:"metadata"`
	Target     DeploymentTarget  `json:"target" yaml:"target"`
	Components map[string]string `json:"components,omitempty" yaml:"components,omitempty"`
}

func (rd *ReleaseDeployment) UnmarshalYAML(unmarshal func(any) error) error {
	// current file structure for release.yaml
	var fileData struct {
		Branch           string   `yaml:"branch"`
		Timestamp        string   `yaml:"timestamp"`
		PullRequestID    int      `yaml:"pullRequestId"`
		Revision         string   `yaml:"revision"`
		UpstreamRevision string   `yaml:"upstreamRevision"`
		Cloud            string   `yaml:"cloud"`
		Environment      string   `yaml:"environment"`
		RegionConfigs    []string `yaml:"regionConfigs"`
		ServiceGroupBase string   `yaml:"serviceGroupBase"`
		ServiceGroup     string   `yaml:"serviceGroup"`
	}

	if err := unmarshal(&fileData); err != nil {
		return err
	}

	// Map to ReleaseDeployment structure
	rd.Metadata = ReleaseMetadata{
		ReleaseId: ReleaseId{
			SourceRevision:   fileData.UpstreamRevision,
			PipelineRevision: fileData.Revision,
		},
		Branch:           fileData.Branch,
		Timestamp:        fileData.Timestamp,
		PullRequestID:    fileData.PullRequestID,
		ServiceGroup:     fileData.ServiceGroup,
		ServiceGroupBase: fileData.ServiceGroupBase,
	}

	rd.Target = DeploymentTarget{
		Cloud:         fileData.Cloud,
		Environment:   fileData.Environment,
		RegionConfigs: fileData.RegionConfigs,
	}

	rd.Components = make(Components)

	return nil
}

type ReleaseDeploymentList struct {
	Items []ReleaseDeployment `json:"items" yaml:"items"`
}

// yamlReleaseMetadata is used for backward compatibility
type yamlReleaseMetadata struct {
	Branch           string `yaml:"branch"`
	Timestamp        string `yaml:"timestamp"`
	PullRequestID    int    `yaml:"pullRequestId"`
	ServiceGroup     string `yaml:"serviceGroup"`
	Revision         string `yaml:"revision"`         // old name
	UpstreamRevision string `yaml:"upstreamRevision"` // old name
	SourceRevision   string `yaml:"sourceRevision"`   // new name (future)
	PipelineRevision string `yaml:"pipelineRevision"` // new name (future)
}
