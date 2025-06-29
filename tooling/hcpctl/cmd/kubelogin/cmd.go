package kubelogin

import (
	"github.com/Azure/kubelogin/pkg/cmd"
	"github.com/spf13/cobra"
)

func NewCommand() (*cobra.Command, error) {
	// Create the kubelogin root command directly from the library
	kubeloginCmd := cmd.NewRootCmd("embedded")

	// Update the command to work as a subcommand
	kubeloginCmd.Use = "kubelogin"
	kubeloginCmd.Short = "Azure Active Directory authentication for Kubernetes"
	kubeloginCmd.Long = "Login to Azure Active Directory and populate kubeconfig with AAD tokens"

	return kubeloginCmd, nil
}
