# Configuring GitHub actions and Azure Integration

## Background

We create an enterprise application and then federate its credentials to this GitHub repository to allow it to login to our subscription.  It can then perform actions within our subscription during GitHub action PR runs. 

## Setup
The steps below are the same outlined [here](https://learn.microsoft.com/en-us/azure/developer/github/connect-from-azure?tabs=azure-portal%2Clinux)

1. login to the ARO HCP E2E Subscription
1. Run the hack script `./tooling/azure-automation/github-actions/hack/create-application.sh`
1. Create the [GitHub secrets](https://learn.microsoft.com/en-us/azure/developer/github/connect-from-azure?tabs=azure-portal%2Clinux#create-github-secrets) based on the output of the script.

Now you can leverage the identity with a contributor role in our GitHub actions.  A sample is [here](./.github/workflows/bicep-what-if.yml)