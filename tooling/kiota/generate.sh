#!/bin/bash

set -euo pipefail

# Main function
main() {
    echo "Starting Kiota SDK generation..."

    verify_kiota
    generate_microsoft_graph_sdk

    echo "All SDK generation complete!"
}


verify_kiota() {
    if ! command -v kiota >/dev/null 2>&1; then
        echo "Error: kiota is not installed or not in PATH." >&2
        exit 1
    fi
}

# Generate Microsoft Graph SDK
generate_microsoft_graph_sdk() {
    echo "Generating Microsoft Graph SDK..."

    kiota generate \
        --clean-output \
        -l go \
        -o ./internal/graph/graphsdk \
        -n "github.com/Azure/ARO-HCP/internal/graph/graphsdk" \
        -d https://raw.githubusercontent.com/microsoftgraph/msgraph-metadata/master/openapi/v1.0/openapi.yaml \
        -c GraphBaseServiceClient \
        --additional-data=False \
        --backing-store=True \
        --include-path "/applications" \
        --include-path "/applications/{application-id}" \
        --include-path "/applications/{application-id}/addPassword" \
        --include-path "/applications/{application-id}/removePassword" \
        --include-path "/groups"

    # Fix import paths to use correct case
    echo "Fixing import paths..."
    find ./internal/graph/graphsdk -type f -name "*.go" -exec sed -i'' -e 's\github.com/azure/aro-hcp\github.com/Azure/ARO-HCP\g' {} +

    echo "Microsoft Graph SDK generation complete!"
}

# Run main function
main "$@"