package list

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/timeparse"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/go-logr/logr"
	"github.com/ohler55/ojg/jp"
	"github.com/spf13/cobra"
	"github.com/stoewer/go-strcase"
	"gopkg.in/yaml.v3"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/tooling/release-explorer/pkg/client/types"
)

const (
	DefaultStorageAccountURL = "https://aroreleases.blob.core.windows.net/"
	DefaultStorageContainer  = "releases"
	ReleaseFileName          = "release.yaml"
	ConfigFileName           = "config.yaml"
	DefaultServiceGroupBase  = "Microsoft.Azure.ARO.HCP"
	DefaultLimit             = 0
)

var (
	DefaultSince = time.Now().Add(-1 * time.Duration(7*24*time.Hour)).UTC()
	DefaultUntil = time.Now().UTC()
)

type Environment string

const (
	ProdEnv Environment = "prod"
	StgEnv  Environment = "stg"
	IntEnv  Environment = "int"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		StorageAccountURI:    DefaultStorageAccountURL,
		StorageContainerName: DefaultStorageContainer,
		Environment:          ProdEnv,
		Since:                DefaultSince,
		Until:                DefaultUntil,
		ServiceGroupBase:     DefaultServiceGroupBase,
		Limit:                DefaultLimit,
	}
}

func (opts *RawOptions) BindOptions(cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.StorageContainerName, "container", opts.StorageContainerName, "Name of the storage container to use.")
	cmd.Flags().StringVar(&opts.ServiceGroupBase, "service-group-base", opts.ServiceGroupBase, "Service group base to use.")
	cmd.Flags().StringVar(&opts.PipelineRevision, "pipeline-rev", opts.PipelineRevision, "Pipeline revision to use.")
	cmd.Flags().StringVar(&opts.SourceRevision, "source-rev", opts.SourceRevision, "Source revision to use.")
	cmd.Flags().BoolVar(&opts.IncludeComponents, "components", opts.IncludeComponents, "Include components in the output.")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "l", opts.Limit, "Limit the number of deployments to return.")

	cmd.Flags().FuncP("account-name", "a", "Name of the storage account to use.", func(s string) error {
		opts.StorageAccountURI = fmt.Sprintf("https://%s.blob.core.windows.net/", s)
		return nil
	})
	cmd.Flags().FuncP("environment", "e", "Environment to use.", func(s string) error {
		opts.Environment = Environment(s)
		return nil
	})
	cmd.Flags().FuncP("since", "s", "Since time to use.", func(s string) error {
		since, err := timeparse.ParseTimeToUTC(s)
		if err != nil {
			return fmt.Errorf("failed to parse since time: %w", err)
		}
		opts.Since = since
		return nil
	})
	cmd.Flags().FuncP("until", "u", "Until time to use.", func(s string) error {
		until, err := timeparse.ParseTimeToUTC(s)
		if err != nil {
			return fmt.Errorf("failed to parse until time: %w", err)
		}
		opts.Until = until
		return nil
	})
	return nil
}

type RawOptions struct {
	StorageAccountURI    string
	StorageContainerName string
	Environment          Environment
	Since                time.Time
	Until                time.Time
	ServiceGroupBase     string
	PipelineRevision     string
	SourceRevision       string
	IncludeComponents    bool
	Limit                int
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	ServiceClient        *service.Client
	Environment          Environment
	Since                time.Time
	Until                time.Time
	ServiceGroupBase     string
	PipelineRevision     string
	SourceRevision       string
	StorageAccountURI    string
	StorageContainerName string
	IncludeComponents    bool
	Limit                int
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "service-account", name: "service account", value: &o.StorageAccountURI},
		{flag: "service-container", name: "service container", value: &o.StorageContainerName},
		{flag: "since", name: "since time", value: ptr.To(o.Since.Format(time.RFC3339))},
		{flag: "until", name: "until time", value: ptr.To(o.Until.Format(time.RFC3339))},
		{flag: "environment", name: "environment", value: ptr.To(string(o.Environment))},
		{flag: "service-group-base", name: "service group base", value: &o.ServiceGroupBase},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	switch o.Environment {
	case ProdEnv, StgEnv, IntEnv:
	default:
		return nil, fmt.Errorf("invalid environment: %s", o.Environment)
	}

	if o.Since.After(o.Until) {
		return nil, fmt.Errorf("since must be before until")
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	azCredential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	serviceClient, err := service.NewClient(o.StorageAccountURI, azCredential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create service client: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			ServiceClient:        serviceClient,
			Environment:          o.Environment,
			Since:                o.Since,
			Until:                o.Until,
			ServiceGroupBase:     o.ServiceGroupBase,
			PipelineRevision:     o.PipelineRevision,
			SourceRevision:       o.SourceRevision,
			IncludeComponents:    o.IncludeComponents,
			StorageAccountURI:    o.StorageAccountURI,
			StorageContainerName: o.StorageContainerName,
			Limit:                o.Limit,
		},
	}, nil
}

// ListReleaseDeployments lists release deployments using tag-based filtering
func (opts *Options) ListReleaseDeployments(ctx context.Context) ([]*types.ReleaseDeployment, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get logger: %w", err)
	}

	tagFilter, err := opts.buildODataFilter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build filter: %w", err)
	}

	type blobWithTime struct {
		containerName string
		name          string
		tags          map[string]string
		timestamp     time.Time
	}

	var blobs []blobWithTime
	var marker *string
	for {
		resp, err := opts.ServiceClient.FilterBlobs(ctx, tagFilter, &service.FilterBlobsOptions{
			Marker: marker,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to filter blobs: %w", err)
		}

		if resp.FilterBlobSegment.Blobs != nil {
			for _, blob := range resp.FilterBlobSegment.Blobs {
				if !strings.HasSuffix(*blob.Name, "/"+ReleaseFileName) {
					continue
				}

				tags := make(map[string]string)
				if blob.Tags != nil && blob.Tags.BlobTagSet != nil {
					for _, tag := range blob.Tags.BlobTagSet {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = *tag.Value
						}
					}
				}

				timestampStr, ok := tags["timestamp"]
				if !ok {
					logger.Error(errors.New("no timestamp found for blob"), "missing timestamp tag", "blob", *blob.Name)
					continue
				}
				timestamp, err := time.Parse(time.RFC3339, timestampStr)
				if err != nil {
					logger.Error(err, "failed to parse timestamp", "blob", *blob.Name)
					continue
				}

				blobs = append(blobs, blobWithTime{
					containerName: *blob.ContainerName,
					name:          *blob.Name,
					tags:          tags,
					timestamp:     timestamp,
				})
			}
		}

		if resp.NextMarker == nil || len(*resp.NextMarker) == 0 {
			break
		}
		marker = resp.NextMarker
	}

	if len(blobs) == 0 {
		return []*types.ReleaseDeployment{}, nil
	}

	sort.Slice(blobs, func(i, j int) bool {
		return blobs[i].timestamp.After(blobs[j].timestamp)
	})

	if opts.Limit > 0 && opts.Limit < len(blobs) {
		blobs = blobs[:opts.Limit]
	}

	// Download and parse each release
	deployments := make([]*types.ReleaseDeployment, 0, len(blobs))
	for _, blob := range blobs {
		deployment, err := opts.downloadAndParseRelease(ctx, blob.name)
		if err != nil {
			logger.Error(err, "failed to download and parse release", "blob", blob.name)
			continue
		}

		deployments = append(deployments, deployment)
	}

	return deployments, nil
}

func (opts *Options) buildODataFilter(ctx context.Context) (string, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logger: %w", err)
	}

	// Build OData filter
	// Format: @container='releases' AND "timestamp" => '2025-10-16T00:00:00Z' AND "timestamp" < '2025-10-31T00:00:00Z' AND "environment"='int' AND "serviceGroupBase"='Microsoft.Azure.ARO.HCP' AND "serviceGroup" >= ''
	// The serviceGroup >= '' condition is always true, but including it causes Azure to return that tag in the response
	filters := []struct {
		key      string
		value    string
		operator string
		enabled  bool
	}{
		{key: "environment", value: string(opts.Environment), operator: "=", enabled: true},
		{key: "serviceGroupBase", value: opts.ServiceGroupBase, operator: "=", enabled: true},
		{key: "timestamp", value: opts.Since.Format(time.RFC3339), operator: ">=", enabled: true},
		{key: "timestamp", value: opts.Until.Format(time.RFC3339), operator: "<", enabled: true},
		{key: "serviceGroup", value: "", operator: ">=", enabled: true},
		{key: "revision", value: opts.PipelineRevision, operator: "=", enabled: opts.PipelineRevision != ""},
		{key: "upstreamRevision", value: opts.SourceRevision, operator: "=", enabled: opts.SourceRevision != ""},
	}

	filter := make([]string, 0, len(filters))
	filter = append(filter, fmt.Sprintf("@container='%s'", opts.StorageContainerName))
	for _, item := range filters {
		if item.enabled {
			filter = append(filter, fmt.Sprintf("\"%s\"%s'%s'", item.key, item.operator, item.value))
		}
	}

	logger.V(1).Info("filter", "filter", strings.Join(filter, " AND "))
	return strings.Join(filter, " AND "), nil
}

func (opts *Options) downloadAndParseRelease(ctx context.Context, blobName string) (*types.ReleaseDeployment, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get logger: %w", err)
	}

	downloadResponse, err := opts.ServiceClient.NewContainerClient(opts.StorageContainerName).
		NewBlobClient(blobName).DownloadStream(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob: %w", err)
	}
	defer func() {
		if err := downloadResponse.Body.Close(); err != nil {
			logger.Error(err, "failed to close blob body", "blob", blobName)
		}
	}()

	content, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read blob content: %w", err)
	}

	var deployment types.ReleaseDeployment
	if err := yaml.Unmarshal(content, &deployment); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	if opts.IncludeComponents && len(deployment.Target.RegionConfigs) > 0 {
		// we naively assume that there's only one region per target
		// TODO: clarify this assumption
		components, err := opts.downloadAndParseComponents(ctx, blobName, deployment.Target.RegionConfigs[0])
		if err != nil {
			return nil, fmt.Errorf("failed to download and parse components: %w", err)
		}
		deployment.Components = components
	}

	return &deployment, nil
}

func (opts *Options) downloadAndParseComponents(ctx context.Context, releasePath, region string) (types.Components, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get logger: %w", err)
	}

	blobName := strings.Join([]string{filepath.Dir(releasePath), region, ConfigFileName}, "/")
	downloadResponse, err := opts.ServiceClient.NewContainerClient(opts.StorageContainerName).
		NewBlobClient(blobName).DownloadStream(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download config: %w", err)
	}
	defer func() {
		if err := downloadResponse.Body.Close(); err != nil {
			logger.Error(err, "failed to close blob body", "blob", blobName)
		}
	}()

	content, err := io.ReadAll(downloadResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	// Parse YAML
	var data any
	if err := yaml.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Extract components using a JSONPath expression
	components := types.Components{}
	digestExpr, err := jp.ParseString("$..image['digest','sha']")
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSONPath expression: %w", err)
	}

	// Walk all digest/sha matches - callback is called once per match
	var errs []error
	digestExpr.Walk(data, func(path jp.Expr, nodes []any) {
		if len(nodes) == 0 {
			errs = append(errs, fmt.Errorf("no nodes found for path: %s", path.String()))
			return
		}

		var values []any
		if values = path.Get(data); len(values) == 0 {
			errs = append(errs, fmt.Errorf("no values found for path: %s", path.String()))
			return
		}

		if values[0] == nil {
			errs = append(errs, fmt.Errorf("value is nil for path: %s", path.String()))
			return
		}

		digest := strings.TrimPrefix(values[0].(string), "sha256:")

		componentName, err := deriveComponentName(path.String())
		if err != nil {
			errs = append(errs, err)
			return
		}
		components[componentName] = digest
	})

	if len(errs) > 0 {
		logger.Error(errors.Join(errs...), "errors encountered while inspecting release components")
	}

	return components, nil
}

// deriveComponentName converts a JSONPath to a component name
func deriveComponentName(path string) (string, error) {
	var componentBasePath string
	if strings.HasSuffix(path, ".image.digest") {
		componentBasePath = strings.TrimSuffix(path, ".image.digest")
	} else if strings.HasSuffix(path, ".image.sha") {
		componentBasePath = strings.TrimSuffix(path, ".image.sha")
	} else {
		return "", fmt.Errorf("invalid path: %s", path)
	}

	parts := strings.Split(strings.TrimPrefix(componentBasePath, "$."), ".")
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid path: %s", path)
	}

	if len(parts) > 0 {
		for part := range parts {
			parts[part] = strcase.KebabCase(parts[part])
		}
	}

	return strings.Join(parts, "."), nil
}
