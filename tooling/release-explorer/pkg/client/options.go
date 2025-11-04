package client

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

const (
	DefaultStorageAccountURL = "https://arodeploymentmetadata.blob.core.windows.net/"
	DefaultStorageContainer  = "aroreleases"
)

type ClientOptions struct {
	StorageAccountURI    string
	StorageContainerName string
}

func DefaultClientOptions() *ClientOptions {
	return &ClientOptions{
		StorageAccountURI:    DefaultStorageAccountURL,
		StorageContainerName: DefaultStorageContainer,
	}
}

type ClientOption func(*ClientOptions)

func WithStorageAccountURI(uri string) ClientOption {
	return func(o *ClientOptions) {
		o.StorageAccountURI = uri
	}
}

func WithStorageContainerName(name string) ClientOption {
	return func(o *ClientOptions) {
		o.StorageContainerName = name
	}
}

// Client handles release operations
type Client struct {
	blobClient *azblob.Client
	container  string
}

// NewClient creates a release client (simple, no validated options needed)
func NewClient(opts ...ClientOption) (*Client, error) {
	options := DefaultClientOptions()
	for _, opt := range opts {
		opt(options)
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct Azure credential: %w", err)
	}

	client, err := azblob.NewClient(options.StorageAccountURI, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct blob client: %w", err)
	}

	return &Client{
		blobClient: client,
		container:  options.StorageContainerName,
	}, nil
}

type ListReleaseTargetsOptions struct {
	Limit        int
	Cloud        string
	Environment  string
	ServiceGroup string
}

func DefaultListReleaseTargetsOptions() *ListReleaseTargetsOptions {
	return &ListReleaseTargetsOptions{
		Limit: 10,
	}
}

type ListReleaseTargetsOption func(*ListReleaseTargetsOptions)

func WithLimit(limit int) ListReleaseTargetsOption {
	return func(o *ListReleaseTargetsOptions) {
		o.Limit = limit
	}
}

func WithCloud(cloud string) ListReleaseTargetsOption {
	return func(o *ListReleaseTargetsOptions) {
		o.Cloud = cloud
	}
}

func WithEnvironment(env string) ListReleaseTargetsOption {
	return func(o *ListReleaseTargetsOptions) {
		o.Environment = env
	}
}

func WithServiceGroup(sg string) ListReleaseTargetsOption {
	return func(o *ListReleaseTargetsOptions) {
		o.ServiceGroup = sg
	}
}

func WithRevision(revision string) ListReleaseTargetsOption {
	return func(o *ListReleaseTargetsOptions) {
		o.Revision = revision
	}
}

func WithUpstreamRevision(upstreamRevision string) ListReleaseTargetsOption {
	return func(o *ListReleaseTargetsOptions) {
		o.UpstreamRevision = upstreamRevision
	}
}

func (c *Client) ListReleaseTargets(ctx context.Context, opts ...ListReleaseTargetsOption) (*ReleaseTargetsList, error) {
	// Apply options
	options := DefaultListReleaseTargetsOptions()
	for _, opt := range opts {
		opt(options)
	}

	return nil, nil
}

func blobContainerPrefixBuilder(opts ...ListReleaseTargetsOption) string {
	options := &ListReleaseTargetsOptions{}
	for _, opt := range opts {
		opt(options)
	}

	return fmt.Sprintf("%s/releases/%s/%s/%s-%s",
		options.ServiceGroup,
		options.Cloud,
		options.Environment,
		options.Revision,
		options.UpstreamRevision,
	)
}

// // --- GetRelease Operation ---

// type GetReleaseOptions struct {
// 	IncludeMetadata  bool
// 	IncludeManifests bool
// }

// type GetReleaseOption func(*GetReleaseOptions)

// func WithMetadata() GetReleaseOption {
// 	return func(o *GetReleaseOptions) {
// 		o.IncludeMetadata = true
// 	}
// }

// func WithManifests() GetReleaseOption {
// 	return func(o *GetReleaseOptions) {
// 		o.IncludeManifests = true
// 	}
// }

// func (c *Client) GetRelease(ctx context.Context, releaseID string, opts ...GetReleaseOption) (*Release, error) {
// 	// Apply options
// 	options := &GetReleaseOptions{}
// 	for _, opt := range opts {
// 		opt(options)
// 	}

// 	// Implementation
// 	// ...
// 	return nil, nil
// }

// // Release represents a release
// type Release struct {
// 	ID           string
// 	Cloud        string
// 	Environment  string
// 	ServiceGroup string
// 	Revision     string
// 	// ... other fields
// }
