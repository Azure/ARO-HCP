package client

import (
	"time"
)

type ContainerImage struct {
	Digest     string `json:"digest"`
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
}

type Component struct {
	Name                     string `json:"name"`
	ImageInfo                ContainerImage
	ImageCreationTime        *time.Time `json:"imageCreationTime,omitempty"`
	RepoURL                  *string    `json:"RepoURL"`
	SourceSHA                string     `json:"sourceSHA"`
	PermanentURLForSourceSHA *string    `json:"permanentURLForSourceSHA,omitempty"`
}

type ReleaseTarget struct {
	Revision         string                `json:"revision"`
	Branch           string                `json:"branch"`
	UpstreamRevision string                `json:"upstreamRevision"`
	Timestamp        string                `json:"timestamp"`
	PullRequestID    int                   `json:"pullRequestId"`
	Cloud            string                `json:"cloud"`
	Environment      string                `json:"environment"`
	ServiceGroup     string                `json:"serviceGroup"`
	ServiceGroupBase string                `json:"serviceGroupBase"`
	Regions          []string              `json:"regions"`
	Components       map[string]*Component `json:"components"`
}

type ReleaseTargetsList struct {
	Items []ReleaseTarget `json:"items"`
}
