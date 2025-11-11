package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/ohler55/ojg/jp"
	"gopkg.in/yaml.v3"
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

type ListReleaseDeploymentsOptions struct {
	Limit             int
	Cloud             string
	Environment       string
	ServiceGroup      string
	ReleaseId         ReleaseId
	IncludeComponents bool
}

func DefaultListReleaseDeploymentsOptions() *ListReleaseDeploymentsOptions {
	return &ListReleaseDeploymentsOptions{
		Limit: 10,
	}
}

type ListReleaseDeploymentsOption func(*ListReleaseDeploymentsOptions)

func WithLimit(limit int) ListReleaseDeploymentsOption {
	return func(o *ListReleaseDeploymentsOptions) {
		o.Limit = limit
	}
}

func WithCloud(cloud string) ListReleaseDeploymentsOption {
	return func(o *ListReleaseDeploymentsOptions) {
		o.Cloud = cloud
	}
}

func WithEnvironment(env string) ListReleaseDeploymentsOption {
	return func(o *ListReleaseDeploymentsOptions) {
		o.Environment = env
	}
}

func WithServiceGroup(sg string) ListReleaseDeploymentsOption {
	return func(o *ListReleaseDeploymentsOptions) {
		o.ServiceGroup = sg
	}
}

func WithRevisionId(releaseId ReleaseId) ListReleaseDeploymentsOption {
	return func(o *ListReleaseDeploymentsOptions) {
		o.ReleaseId = releaseId
	}
}

func WithIncludeComponents(include bool) ListReleaseDeploymentsOption {
	return func(o *ListReleaseDeploymentsOptions) {
		o.IncludeComponents = include
	}
}

func (c *Client) ListReleaseDeployments(ctx context.Context, opts ...ListReleaseDeploymentsOption) ([]*ReleaseDeployment, error) {
	options := DefaultListReleaseDeploymentsOptions()
	for _, opt := range opts {
		opt(options)
	}

	prefix, err := blobContainerPrefixBuilder(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to build prefix: %w", err)
	}

	type blobCandidate struct {
		name      string
		timestamp string
	}
	var candidates []blobCandidate

	includeFlags := azblob.ListBlobsInclude{
		Tags: true,
	}

	pager := c.blobClient.NewListBlobsFlatPager(c.container, &azblob.ListBlobsFlatOptions{
		Prefix:  &prefix,
		Include: includeFlags,
	})

	// TODO: this right now gets all pages to then sort by the timestamp tag
	// This won't scale well, we need to add the timestamp tag to the blob path itself
	// for efficient blob operations
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list blobs: %w", err)
		}

		for _, blobItem := range page.Segment.BlobItems {
			if !strings.HasSuffix(*blobItem.Name, ReleaseFileName) {
				continue
			}

			candidates = append(candidates, blobCandidate{
				name:      *blobItem.Name,
				timestamp: getTagValue(blobItem, "timestamp"),
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].timestamp > candidates[j].timestamp
	})

	limit := min(options.Limit, len(candidates))

	results := make([]*ReleaseDeployment, 0, limit)
	for _, candidate := range candidates[:limit] {
		deployment, err := c.downloadAndParseRelease(ctx, candidate.name, options.IncludeComponents)
		if err != nil {
			// TODO: use a proper logger
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", candidate.name, err)
			continue
		}
		results = append(results, deployment)
	}

	return results, nil
}

// downloadAndParseRelease downloads and parses a single release.yaml file
func (c *Client) downloadAndParseRelease(ctx context.Context, blobName string, includeComponents bool) (*ReleaseDeployment, error) {
	// Download the blob content
	downloadResponse, err := c.blobClient.DownloadStream(ctx, c.container, blobName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob: %w", err)
	}
	defer downloadResponse.Body.Close()

	content, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read blob content: %w", err)
	}

	deployment := &ReleaseDeployment{}
	if err := yaml.Unmarshal(content, deployment); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	if includeComponents {
		// we naively assume that there's only one region per target
		// TODO: clarify this assumption
		components, err := c.downloadAndParseConfig(ctx, blobName, deployment.Target.RegionConfigs[0])
		if err != nil {
			return nil, fmt.Errorf("failed to download and parse config: %w", err)
		}
		deployment.Components = components
	}
	return deployment, nil
}

func (c *Client) downloadAndParseConfig(ctx context.Context, releasePath, region string) (map[string]*Component, error) {
	blobName := strings.Join([]string{strings.TrimSuffix(releasePath, "/"+ReleaseFileName), region, ConfigFileName}, "/")
	downloadResponse, err := c.blobClient.DownloadStream(ctx, c.container, blobName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob: %w", err)
	}
	defer downloadResponse.Body.Close()

	content, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read blob content: %w", err)
	}

	var data any
	if err := yaml.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	components := make(map[string]*Component)

	digestExpr, err := jp.ParseString("$..image['digest','sha']")
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSONPath expression: %w", err)
	}

	// Walk all digest matches - callback is called once per match
	digestExpr.Walk(data, func(path jp.Expr, nodes []any) {
		if len(nodes) == 0 {
			return
		}

		var digest string
		var values []any
		if values = path.Get(data); len(values) == 0 {
			return
		}

		if values[0] != nil {
			digest = strings.TrimPrefix(values[0].(string), "sha256:")
		}

		component := extractComponentFromPath(path, digest)
		if component != nil {
			components[component.Name] = component
		}
	})

	return components, nil
}

// extractComponentFromPath extracts component info given a JSONPath to a digest/sha field
func extractComponentFromPath(path jp.Expr, digest string) *Component {
	// Path is like: ["svc", "prometheus", "prometheusOperator", "image", "digest"]
	// We want the parent (the "image" object)
	var componentBasePath string
	if strings.HasSuffix(path.String(), ".image.digest") {
		componentBasePath = strings.TrimSuffix(path.String(), ".image.digest")
	} else if strings.HasSuffix(path.String(), ".image.sha") {
		componentBasePath = strings.TrimSuffix(path.String(), ".image.sha")
	} else {
		return nil
	}

	componentName := deriveComponentName(componentBasePath)

	return &Component{
		Name: componentName,
		ImageInfo: ContainerImage{
			Digest: digest,
		},
	}
}

// deriveComponentName converts a JSONPath to a component name
// Example: "$.svc.prometheus.prometheusOperator" -> "svc.prometheus.prometheus-operator"
func deriveComponentName(basePath string) string {
	// Remove "$." prefix if present
	basePath = strings.TrimPrefix(basePath, "$.")

	// Split by "."
	parts := strings.Split(basePath, ".")
	if len(parts) == 0 {
		return "unknown"
	}

	// Convert the last part from camelCase to kebab-case
	if len(parts) > 0 {
		parts[len(parts)-1] = camelToKebab(parts[len(parts)-1])
	}

	return strings.Join(parts, ".")
}

// camelToKebab converts camelCase to kebab-case
// Example: "prometheusOperator" -> "prometheus-operator"
func camelToKebab(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			result.WriteRune('-')
		}
		result.WriteRune(unicode.ToLower(r))
	}
	return result.String()
}

func blobContainerPrefixBuilder(opts ...ListReleaseDeploymentsOption) (string, error) {
	options := &ListReleaseDeploymentsOptions{}
	for _, opt := range opts {
		opt(options)
	}

	if options.ServiceGroup == "" {
		return "", fmt.Errorf("service group is required")
	}

	prefix := fmt.Sprintf("%s/releases", options.ServiceGroup)

	if options.Cloud != "" {
		prefix = fmt.Sprintf("%s/%s", prefix, options.Cloud)

		if options.Environment != "" {
			prefix = fmt.Sprintf("%s/%s", prefix, options.Environment)

			hasSourceRev := options.ReleaseId.SourceRevision != ""
			hasPipelineRev := options.ReleaseId.PipelineRevision != ""
			if hasSourceRev != hasPipelineRev {
				return "", fmt.Errorf("incomplete release ID: both SourceRevision and PipelineRevision must be provided")
			}

			if hasSourceRev && hasPipelineRev {
				prefix = fmt.Sprintf("%s/%s", prefix, options.ReleaseId.String())
			}
		}
	}

	return prefix, nil
}

// Helper function to extract a tag value by key
func getTagValue(blobItem *container.BlobItem, key string) string { // Change here
	if blobItem.BlobTags == nil || blobItem.BlobTags.BlobTagSet == nil {
		return ""
	}

	for _, tag := range blobItem.BlobTags.BlobTagSet {
		if tag.Key != nil && *tag.Key == key && tag.Value != nil {
			return *tag.Value
		}
	}
	return ""
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
