#!/bin/bash

# Common functions for mock FPA scripts
# This file contains shared logic for dev-application.sh and test-mock-fpa-policies.sh

# Set up dedicated Azure CLI config directory and file paths
setupAzureConfig() {
    local script_dir="$1"

    AZURE_CONFIG_DIR="$script_dir/../mock-fpa-azure-config"
    export AZURE_CONFIG_DIR

    # Create Azure config directory if it doesn't exist
    mkdir -p "$AZURE_CONFIG_DIR"

    # Define certificate file paths with mock-fpa- prefix in mock-fpa-azure-config directory
    MOCK_FPA_PFX_FILE="$AZURE_CONFIG_DIR/mock-fpa-app.pfx"
    MOCK_FPA_PEM_FILE="$AZURE_CONFIG_DIR/mock-fpa-app.pem"

    export MOCK_FPA_PFX_FILE
    export MOCK_FPA_PEM_FILE
}

# Login with mock service principal using certificates from mock-fpa-azure-config directory
loginWithMockServicePrincipal() {
    local fp_certificate_name="$1"
    local key_vault_name="$2"
    local fp_application_name="$3"

    if [[ -z "$fp_certificate_name" || -z "$key_vault_name" || -z "$fp_application_name" ]]; then
        echo "Error: Missing required parameters for loginWithMockServicePrincipal"
        echo "Usage: loginWithMockServicePrincipal <certificate_name> <key_vault_name> <application_name>"
        return 1
    fi

    # Ensure azure config directory and file paths are set up
    if [[ -z "$MOCK_FPA_PFX_FILE" || -z "$MOCK_FPA_PEM_FILE" ]]; then
        echo "Error: Mock FPA file paths not initialized. Call setupAzureConfig first."
        return 1
    fi

    echo "Downloading certificate from Key Vault to: $MOCK_FPA_PFX_FILE"
    az keyvault secret download \
        --name "$fp_certificate_name" \
        --vault-name "$key_vault_name" \
        --encoding base64 \
        --file "$MOCK_FPA_PFX_FILE"

    echo "Converting certificate to PEM format: $MOCK_FPA_PEM_FILE"
    openssl pkcs12 \
        -in "$MOCK_FPA_PFX_FILE" \
        -passin pass: \
        -out "$MOCK_FPA_PEM_FILE" \
        -nodes

    local appId=$(az ad app list --display-name "$fp_application_name" --query "[0].appId" -o tsv)
    local tenantId=$(az account show --query tenantId -o tsv)

    echo "Logging in as mock FPA service principal (App ID: $appId)"
    az login --service-principal -u "$appId" --certificate "$MOCK_FPA_PEM_FILE" --tenant "$tenantId"

    # Note: Not deleting PEM file as Azure CLI needs to reference it for subsequent operations
    # The certificate file will be cleaned up when the session ends
    rm "$MOCK_FPA_PFX_FILE"
    echo "Certificate file $MOCK_FPA_PEM_FILE kept for Azure CLI session"
    echo "Temporary PFX file $MOCK_FPA_PFX_FILE removed"
}

# Clean up certificate files when done with service principal session
cleanupMockFpaCertificateFiles() {
    local files_cleaned=0

    if [[ -f "$MOCK_FPA_PEM_FILE" ]]; then
        echo "Cleaning up certificate file: $MOCK_FPA_PEM_FILE"
        rm "$MOCK_FPA_PEM_FILE"
        ((files_cleaned++))
    fi

    if [[ -f "$MOCK_FPA_PFX_FILE" ]]; then
        echo "Cleaning up certificate file: $MOCK_FPA_PFX_FILE"
        rm "$MOCK_FPA_PFX_FILE"
        ((files_cleaned++))
    fi

    # Also clean up legacy files from current directory (backward compatibility)
    if [[ -f "app.pem" ]]; then
        echo "Cleaning up legacy certificate file: app.pem"
        rm "app.pem"
        ((files_cleaned++))
    fi

    if [[ -f "app.pfx" ]]; then
        echo "Cleaning up legacy certificate file: app.pfx"
        rm "app.pfx"
        ((files_cleaned++))
    fi

    if [[ $files_cleaned -eq 0 ]]; then
        echo "No certificate files found to clean up"
    else
        echo "Cleaned up $files_cleaned certificate file(s)"
    fi
}

# Check if we're already logged in as the expected mock FPA service principal
isLoggedInAsMockFpa() {
    local fp_application_name="$1"

    if [[ -z "$fp_application_name" ]]; then
        echo "Error: Missing application name for isLoggedInAsMockFpa"
        return 1
    fi

    local current_user=$(az account show --query user.name -o tsv 2>/dev/null || echo "")
    local current_type=$(az account show --query user.type -o tsv 2>/dev/null || echo "")

    # Check if we're logged in as a service principal
    if [[ "$current_type" != "servicePrincipal" || -z "$current_user" ]]; then
        return 1
    fi

    # Try to get the expected app ID
    local expected_app_id=""
    if expected_app_id=$(az ad app list --display-name "$fp_application_name" --query "[0].appId" -o tsv 2>/dev/null) && [[ -n "$expected_app_id" ]]; then
        if [[ "$current_user" == "$expected_app_id" ]]; then
            echo "Already logged in as mock FPA service principal: $current_user"
            return 0
        fi
    else
        # Can't verify the exact app ID, but if we're a service principal and the name looks right,
        # we'll assume it's correct to avoid authentication loops
        echo "Already logged in as service principal: $current_user (assuming this is the mock FPA)"
        return 0
    fi

    return 1
}

# Print environment information with azure config directory
printMockFpaEnv() {
    local location="$1"
    local resource_group="$2"
    local subscription_id="$3"
    local key_vault_name="$4"
    local fp_application_name="$5"
    local fp_certificate_name="$6"
    local ah_application_name="$7"
    local ah_certificate_name="$8"

    echo "LOCATION: $location"
    echo "RESOURCE_GROUP: $resource_group"
    echo "SUBSCRIPTION_ID: $subscription_id"
    echo "KEY_VAULT_NAME: $key_vault_name"
    echo "FP_APPLICATION_NAME: $fp_application_name"
    echo "FP_CERTIFICATE_NAME: $fp_certificate_name"
    echo "AH_APPLICATION_NAME: $ah_application_name"
    echo "AH_CERTIFICATE_NAME: $ah_certificate_name"
    echo "AZURE_CONFIG_DIR: $AZURE_CONFIG_DIR"
    echo "MOCK_FPA_CERT_FILES: $MOCK_FPA_PEM_FILE, $MOCK_FPA_PFX_FILE"
}

# Export environment variables for shell usage
exportMockFpaShellEnv() {
    local location="$1"
    local resource_group="$2"
    local subscription_id="$3"
    local key_vault_name="$4"
    local fp_application_name="$5"
    local fp_certificate_name="$6"
    local ah_application_name="$7"
    local ah_certificate_name="$8"

    echo "LOCATION=\"$location\"; export LOCATION"
    echo "RESOURCE_GROUP=\"$resource_group\"; export RESOURCE_GROUP"
    echo "SUBSCRIPTION_ID=\"$subscription_id\"; export SUBSCRIPTION_ID"
    echo "ARO_HCP_DEV_KEY_VAULT_NAME=\"$key_vault_name\"; export ARO_HCP_DEV_KEY_VAULT_NAME"
    echo "ARO_HCP_DEV_FP_APPLICATION_NAME=\"$fp_application_name\"; export ARO_HCP_DEV_FP_APPLICATION_NAME"
    echo "ARO_HCP_DEV_FP_CERTIFICATE_NAME=\"$fp_certificate_name\"; export ARO_HCP_DEV_FP_CERTIFICATE_NAME"
    echo "ARO_HCP_DEV_AH_APPLICATION_NAME=\"$ah_application_name\"; export ARO_HCP_DEV_AH_APPLICATION_NAME"
    echo "ARO_HCP_DEV_AH_CERTIFICATE_NAME=\"$ah_certificate_name\"; export ARO_HCP_DEV_AH_CERTIFICATE_NAME"
    echo "AZURE_CONFIG_DIR=\"$AZURE_CONFIG_DIR\"; export AZURE_CONFIG_DIR"
    echo "MOCK_FPA_PFX_FILE=\"$MOCK_FPA_PFX_FILE\"; export MOCK_FPA_PFX_FILE"
    echo "MOCK_FPA_PEM_FILE=\"$MOCK_FPA_PEM_FILE\"; export MOCK_FPA_PEM_FILE"
}
