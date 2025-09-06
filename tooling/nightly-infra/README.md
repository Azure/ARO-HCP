# Nightly Infrastructure - Container App

Automated Container App that runs nightly infrastructure tasks.

## Quick Start

```bash
export DEPLOY_ENV=ntly  # or dev, prod, etc.
make all
make info  # Check configuration
```

## Files

```
tooling/nightly-infra/
├── Dockerfile                     # Container definition
├── Makefile                      # Build and deploy commands
├── config.tmpl.mk                # Configuration template
├── deploy-infra.sh               # Azure deployment script
└── scripts/deploy.sh             # Container workload script
```

## Configuration

Template-based configuration:
- `config.tmpl.mk` - Template with placeholders like `{{ .acr.svc.name }}`
- `config.mk` - Auto-generated with actual values
- `DEPLOY_ENV` - Environment name (defaults to 'ntly')

## Commands

```bash
make image     # Build container image
make push      # Build and push to registry
make deploy    # Deploy Container App infrastructure
make all       # Build, push, and deploy
make info      # Show configuration
```

## How It Works

1. **Build**: `make all` builds container and deploys infrastructure
2. **Schedule**: Container App runs daily at 2 AM
3. **Tasks**: Executes `make infra.svc` and `make infra.mgmt`

## Manual Deployment

```bash
./deploy-infra.sh [resource-group] [location] [acr-name] [environment]

# Examples:
./deploy-infra.sh                                  # Use defaults
./deploy-infra.sh rg-my-app uksouth myacr prod    # Custom values
```

## Monitoring

```bash
# Start job manually
az containerapp job start --name aro-hcp-ntly-job --resource-group rg-aro-hcp-ntly

# View executions and logs
az containerapp job execution list --name aro-hcp-ntly-job --resource-group rg-aro-hcp-ntly
az containerapp logs show --name aro-hcp-ntly-job --resource-group rg-aro-hcp-ntly
```

## Troubleshooting

```bash
az login && az account show                      # Check Azure login
az acr login --name ${ARO_HCP_IMAGE_ACR}         # Check ACR access
az group show --name rg-aro-hcp-ntly             # Verify resource group
```