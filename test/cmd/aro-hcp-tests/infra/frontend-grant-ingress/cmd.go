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

package frontendgrantingress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/Azure/ARO-HCP/test/pkg/frontend"
	"github.com/Azure/ARO-HCP/test/pkg/publicip"
)

type RawOptions struct {
	SubscriptionID string
	ResourceGroup  string
	SourceIP       string
	Out            io.Writer
}

type validatedOptions struct {
	subscriptionID string
	resourceGroup  string
	source         netip.Addr
	out            io.Writer
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	client        *armnetwork.SecurityRulesClient
	resourceGroup string
	source        netip.Addr
	out           io.Writer
}

type Options struct {
	*completedOptions
}

func DefaultOptions() *RawOptions {
	return &RawOptions{Out: os.Stdout}
}

func BindOptions(opts *RawOptions, command *cobra.Command) {
	flags := command.Flags()
	flags.StringVar(&opts.SubscriptionID, "subscription-id", "", "Azure subscription containing the frontend NSG (required)")
	flags.StringVar(&opts.ResourceGroup, "resource-group", "", "Resource group containing the frontend NSG (required)")
	flags.StringVar(&opts.SourceIP, "source-ip", "", "Single IPv4 address allowed to access frontend HTTPS (required)")
	for _, name := range []string{"subscription-id", "resource-group", "source-ip"} {
		// These flags have just been registered, so their names are known to exist.
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}

func NewCommand() *cobra.Command {
	opts := DefaultOptions()
	command := &cobra.Command{
		Use:          "frontend-grant-ingress",
		Short:        "Allow one explicit IPv4 address to access frontend HTTPS",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			opts.Out = command.OutOrStdout()
			validated, err := opts.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete(command.Context())
			if err != nil {
				return err
			}
			return completed.Run(command.Context())
		},
	}
	BindOptions(opts, command)
	return command
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if strings.TrimSpace(o.SubscriptionID) == "" {
		return nil, fmt.Errorf("--subscription-id is required")
	}
	if strings.TrimSpace(o.ResourceGroup) == "" {
		return nil, fmt.Errorf("--resource-group is required")
	}
	if o.SourceIP == "" {
		return nil, fmt.Errorf("--source-ip is required")
	}
	source, err := publicip.ParseIPv4(o.SourceIP)
	if err != nil {
		return nil, fmt.Errorf("invalid --source-ip: %w", err)
	}
	return &ValidatedOptions{validatedOptions: &validatedOptions{
		subscriptionID: o.SubscriptionID,
		resourceGroup:  o.ResourceGroup,
		source:         source,
		out:            o.Out,
	}}, nil
}

func (o *ValidatedOptions) Complete(_ context.Context) (*Options, error) {
	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("configure Azure credentials: %w", err)
	}
	client, err := armnetwork.NewSecurityRulesClient(o.subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure security rules client: %w", err)
	}
	return &Options{completedOptions: &completedOptions{
		client:        client,
		resourceGroup: o.resourceGroup,
		source:        o.source,
		out:           o.out,
	}}, nil
}

func (o *Options) Run(ctx context.Context) error {
	if err := frontend.GrantIngress(ctx, o.client, o.resourceGroup, o.source); err != nil {
		return err
	}
	if err := json.NewEncoder(o.out).Encode(struct {
		ResourceGroup string `json:"resourceGroup"`
	}{ResourceGroup: o.resourceGroup}); err != nil {
		return fmt.Errorf("write ingress result: %w", err)
	}
	return nil
}
