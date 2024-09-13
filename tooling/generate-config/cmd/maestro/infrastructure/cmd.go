package infrastructure

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/generate-config/cmd/common"
)

func NewCommand(opts *common.RawPrimitiveOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "infrastructure",
		Short:        "Generates configuration for Maestro infrastructure deployments.",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	bicepOpts := common.DefaultBicepOptions()
	common.BindBicepOptions(bicepOpts, cmd.PersistentFlags())
	cmd.AddCommand(newInfrastructureCommand(opts, bicepOpts))
	cmd.AddCommand(newServerCommand(opts, bicepOpts))
	cmd.AddCommand(newConsumerCommand(opts, bicepOpts))

	return cmd
}

func validate(opts *common.RawPrimitiveOptions, bicepOpts *common.RawBicepOptions) (*common.PrimitiveOptions, *common.BicepOptions, error) {
	validOpts, err := opts.Validate()
	if err != nil {
		return nil, nil, err
	}
	completedOpts, err := validOpts.Complete()
	if err != nil {
		return nil, nil, err
	}

	validBicepOpts, err := bicepOpts.Validate()
	if err != nil {
		return nil, nil, err
	}
	completedBicepOpts, err := validBicepOpts.Complete()
	if err != nil {
		return nil, nil, err
	}

	return completedOpts, completedBicepOpts, nil
}

func newInfrastructureCommand(rawOpts *common.RawPrimitiveOptions, rawBicepOpts *common.RawBicepOptions) *cobra.Command {
	return &cobra.Command{
		Use:          "infrastructure",
		Short:        "Generates configuration for Maestro regional infrastructure deployments.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, bicepOpts, err := validate(rawOpts, rawBicepOpts)
			if err != nil {
				return err
			}

			return renderInfrastructure(opts, bicepOpts)
		},
	}
}

func renderInfrastructure(opts *common.PrimitiveOptions, bicepOpts *common.BicepOptions) error {
	parameters := common.BicepParameters{}
	parameters.Register(
		eventGridNamespace(opts),
		keyVaultName(opts),
		keyVaultCertOfficerManagedIdentityName(opts),
	)
	fmt.Fprintf(os.Stdout, parameters.Render(bicepOpts))
	return nil
}

func eventGridNamespace(opts *common.PrimitiveOptions) common.BicepParameter {
	return common.BicepParameter{Key: "eventGridNamespaceName", Value: fmt.Sprintf("maestro-%s", opts.Suffix)}
}

func keyVaultName(opts *common.PrimitiveOptions) common.BicepParameter {
	return common.BicepParameter{Key: "maestroKeyVaultName", Value: fmt.Sprintf("maestro-keyvault-%s-%s", opts.Region, opts.Suffix)}
}

func keyVaultCertOfficerManagedIdentityName(opts *common.PrimitiveOptions) common.BicepParameter {
	return common.BicepParameter{Key: "kvCertOfficerManagedIdentityName", Value: fmt.Sprintf("%s-cert-officer", keyVaultName(opts).Value)}
}

func newServerCommand(rawOpts *common.RawPrimitiveOptions, rawBicepOpts *common.RawBicepOptions) *cobra.Command {
	return &cobra.Command{
		Use:          "server",
		Short:        "Generates configuration for Maestro regional server infrastructure deployments.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, bicepOpts, err := validate(rawOpts, rawBicepOpts)
			if err != nil {
				return err
			}

			return renderServer(opts, bicepOpts)
		},
	}
}

func renderServer(opts *common.PrimitiveOptions, bicepOpts *common.BicepOptions) error {
	parameters := common.BicepParameters{}
	parameters.Register(
		common.Location(opts),
		infraResourceGroup(opts),
		eventGridNamespace(opts),
		keyVaultName(opts),
		keyVaultCertOfficerManagedIdentityName(opts),
		keyVaultCertificateDomain(opts),
		postgresServerName(opts),
	)
	// TODO: do we really need IDs or can we use names?
	//maestroServerManagedIdentityPrincipalId: filter(
	//svcCluster.outputs.userAssignedIdentities,
	//id => id.uamiName == 'maestro-server'
	//)[0].uamiPrincipalID
	//maestroServerManagedIdentityName: filter(
	//svcCluster.outputs.userAssignedIdentities,
	//id => id.uamiName == 'maestro-server'
	//)[0].uamiName
	fmt.Fprintf(os.Stdout, parameters.Render(bicepOpts))
	return nil
}

func infraResourceGroup(opts *common.PrimitiveOptions) common.BicepParameter {
	// TODO: maybe we don't have a special parameter with a different name?
	raw := common.RegionalResourceGroup(opts)
	raw.Key = "maestroInfraResourceGroup"
	return raw
}

func keyVaultCertificateDomain(opts *common.PrimitiveOptions) common.BicepParameter {
	return common.BicepParameter{Key: "maestroKeyVaultCertificateDomain", Value: fmt.Sprintf("selfsigned.maestro.keyvault.aro-%s.azure.com", opts.Environment)}
}

func postgresServerName(opts *common.PrimitiveOptions) common.BicepParameter {
	return common.BicepParameter{Key: "postgresServerName", Value: fmt.Sprintf("cluster-service-%s", opts.Environment)}
}

func newConsumerCommand(rawOpts *common.RawPrimitiveOptions, rawBicepOpts *common.RawBicepOptions) *cobra.Command {
	return &cobra.Command{
		Use:          "consumer",
		Short:        "Generates configuration for Maestro consumer infrastructure deployments.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, bicepOpts, err := validate(rawOpts, rawBicepOpts)
			if err != nil {
				return err
			}

			return renderConsumer(opts, bicepOpts)
		},
	}
}

func renderConsumer(opts *common.PrimitiveOptions, bicepOpts *common.BicepOptions) error {
	parameters := common.BicepParameters{}
	parameters.Register(
		eventGridNamespace(opts),
		keyVaultName(opts),
		keyVaultCertOfficerManagedIdentityName(opts),
	)
	fmt.Fprintf(os.Stdout, parameters.Render(bicepOpts))
	return nil
}
