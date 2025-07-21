# Configuring GitHub actions and Azure Integration

## Background

We create an Azure AD application and service principal, then federate its credentials to this GitHub repository. This allows GitHub Actions workflows (for both pull requests and the main branch) to authenticate to Azure using OIDC, enabling them to perform actions within our subscription.

The setup script now provides enhanced functionality:

-   **Multi-environment support**: Automatically configures role assignments across multiple environments (dev, int, stage)
-   **Idempotent application creation**: Reuses existing Azure AD applications and service principals when available
-   **Comprehensive role assignments**: Assigns multiple roles (Contributor, RBAC Admin, User Access Admin, Grafana Admin, Key Vault roles, and AKS RBAC Cluster Admin) across relevant subscriptions
-   **Complete mode deployment**: Uses complete deployment mode for automation accounts to avoid orphaned resources.

## Setup

The steps below are the same outlined [here](https://learn.microsoft.com/en-us/azure/developer/github/connect-from-azure?tabs=azure-portal%2Clinux)

1. Login to the destination Subscription (example: "ARO HCP E2E", "ARO Hosted Control Planes (EA Subscription 1)")
1. Run the hack script `./tooling/azure-automation/github-actions/hack/ "NAME_OF_SUBSCRIPTION"`
1. Create the [GitHub secrets](https://learn.microsoft.com/en-us/azure/developer/github/connect-from-azure?tabs=azure-portal%2Clinux#create-github-secrets) based on the output of the script.

Now you can leverage the identity with a contributor role in our GitHub actions. A sample is [here](./.github/workflows/bicep-what-if.yml)
