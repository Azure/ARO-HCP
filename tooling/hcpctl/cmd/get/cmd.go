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

// Package getcmd provides the Kusto-backed hcpctl get command.
package getcmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/common"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kubeget"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

const defaultQueryTimeout = 5 * time.Minute

var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

type Options struct {
	Server           string
	Cluster          string
	Namespace        string
	Output           string
	AllNamespaces    bool
	ShowKind         bool
	NoKustoTimestamp bool

	resolveServer func(context.Context, string) (*url.URL, error)
}

func DefaultOptions() *Options {
	return &Options{
		resolveServer: resolveServer,
	}
}

func NewCommand(group string) (*cobra.Command, error) {
	options := DefaultOptions()
	command := &cobra.Command{
		Use:     "get RESOURCE [NAME]",
		Short:   "kubectl get using Kusto as a source",
		GroupID: group,
		Args:    cobra.RangeArgs(1, 2),
		Example: `  hcpctl get pods -A -s my-kusto-cluster -c my-mgmt-cluster
  hcpctl get pod my-pod -n my-namespace -s https://example.eastus2.kusto.windows.net -c my-mgmt-cluster -o yaml
  hcpctl get all -A -s my-kusto-cluster -c my-mgmt-cluster -o wide`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			resource, name, err := parseResourceArgs(args)
			if err != nil {
				return err
			}
			return options.Run(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), resource, name)
		},
	}
	bindFlags(command, options)
	return command, nil
}

func bindFlags(command *cobra.Command, options *Options) {
	command.Flags().StringVarP(&options.Server, "server", "s", "", "Kusto server name, hostname, or URL (required)")
	command.Flags().StringVarP(&options.Cluster, "cluster", "c", "", "Kubernetes cluster name in Kusto (required)")
	command.Flags().StringVarP(&options.Namespace, "namespace", "n", "", "namespace to query (required for namespaced resources unless -A is used)")
	command.Flags().StringVarP(&options.Output, "output", "o", "", "output format: wide, json, yaml, or name")
	command.Flags().BoolVarP(&options.AllNamespaces, "all-namespaces", "A", false, "list resources across all namespaces")
	command.Flags().BoolVar(&options.ShowKind, "show-kind", false, "list the resource type for each object")
	command.Flags().BoolVar(&options.NoKustoTimestamp, "no-kusto-timestamp", false, "do not write the Kusto source timestamp to stderr")
	command.Flags().BoolVar(&options.NoKustoTimestamp, "no-ts", false, "alias for --no-kusto-timestamp")
	command.MarkFlagsMutuallyExclusive("namespace", "all-namespaces")
}

func (o *Options) Run(ctx context.Context, stdout, stderr io.Writer, resource, name string) error {
	if strings.TrimSpace(o.Server) == "" {
		return fmt.Errorf("--server is required")
	}
	if strings.TrimSpace(o.Cluster) == "" {
		return fmt.Errorf("--cluster is required")
	}
	if o.Namespace != "" && !namespacePattern.MatchString(o.Namespace) {
		return fmt.Errorf("invalid namespace %q", o.Namespace)
	}
	output := kubeget.OutputFormat(strings.ToLower(o.Output))
	switch output {
	case kubeget.OutputDefault, kubeget.OutputWide, kubeget.OutputJSON, kubeget.OutputYAML, kubeget.OutputName:
	default:
		return fmt.Errorf("unsupported output format %q (allowed: wide, json, yaml, name)", o.Output)
	}

	endpoint, err := o.resolveServer(ctx, o.Server)
	if err != nil {
		return err
	}
	client, err := kusto.NewClient(endpoint, defaultQueryTimeout)
	if err != nil {
		return err
	}
	defer client.Close()

	getter := kubeget.NewGetter(kubeget.NewKustoRepository(client))
	result, err := getter.Get(ctx, kubeget.Request{
		Cluster:       o.Cluster,
		Resource:      resource,
		Name:          name,
		Namespace:     o.Namespace,
		AllNamespaces: o.AllNamespaces,
		Output:        output,
		ShowKind:      o.ShowKind,
	}, stdout)
	if err != nil {
		return err
	}
	if !o.NoKustoTimestamp {
		return kubeget.WriteSourceMetadata(stderr, result)
	}
	return nil
}

func parseResourceArgs(args []string) (string, string, error) {
	if len(args) == 2 {
		if strings.Contains(args[0], "/") {
			return "", "", fmt.Errorf("resource/name form cannot be combined with a separate name")
		}
		return args[0], args[1], nil
	}
	parts := strings.Split(args[0], "/")
	switch len(parts) {
	case 1:
		return parts[0], "", nil
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("resource and name must not be empty")
		}
		return parts[0], parts[1], nil
	default:
		return "", "", fmt.Errorf("invalid resource argument %q", args[0])
	}
}

func resolveServer(ctx context.Context, value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		endpoint, err := url.Parse(value)
		if err != nil || endpoint.Host == "" {
			return nil, fmt.Errorf("invalid Kusto server URL %q", value)
		}
		return endpoint, nil
	}
	if strings.Contains(value, ".") {
		endpoint, err := url.Parse("https://" + value)
		if err != nil {
			return nil, fmt.Errorf("invalid Kusto server hostname %q: %w", value, err)
		}
		return endpoint, nil
	}

	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create Azure credential for Kusto server discovery: %w", err)
	}
	client, err := armresourcegraph.NewClient(credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create Resource Graph client for Kusto server discovery: %w", err)
	}
	escapedName := strings.ReplaceAll(value, "'", "''")
	query := fmt.Sprintf("resources | where type =~ 'microsoft.kusto/clusters' | where name =~ '%s' | project name, uri=tostring(properties.uri)", escapedName)
	response, err := client.Resources(ctx, armresourcegraph.QueryRequest{Query: &query}, nil)
	if err != nil {
		return nil, fmt.Errorf("resolve short Kusto server name %q with Azure Resource Graph: %w (pass a full hostname or URL to skip discovery)", value, err)
	}
	rows, err := common.ParseResourceGraphResultData(response.Data)
	if err != nil {
		return nil, fmt.Errorf("parse Resource Graph response for Kusto server %q: %w", value, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("server %q was not found by Azure Resource Graph; pass its full hostname or URL", value)
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("server name %q resolved to multiple Kusto resources", value)
	}
	uri := common.ParseStringField(rows[0], "uri")
	endpoint, err := url.Parse(uri)
	if err != nil || endpoint.Host == "" {
		return nil, fmt.Errorf("invalid URI %q returned by Azure Resource Graph for Kusto server %q", uri, value)
	}
	return endpoint, nil
}
