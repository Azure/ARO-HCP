# Shoebox documentation

Shoebox provides customer diagnostic logs for ARO Hosted Control Planes (HCP).
Customers select log categories and destinations through Azure Monitor diagnostic settings.

Use this documentation to understand the system, change its configuration, and investigate missing logs.

## Documents

| Document | Contents |
|---|---|
| [Architecture](architecture.md) | Components, responsibilities, and log flow |
| [Record format](record-format.md) | Resource identity, Lua functions, categories, and output fields |
| [mdsd configuration](mdsd-configuration.md) | Agent settings, certificate access, and namespace XML |
| [Deployment](deployment.md) | Category registration and configuration changes |
| [Troubleshooting](troubleshooting.md) | Inspection procedures and expected results |
| [Verification](verification.md) | Existing tests and their limits |
| [Terms and sources](reference.md) | Technical terms and code references |

## Scope

These documents cover control plane log collection, record transformation, category registration, and customer delivery.
ARO-HCP contains the collection and agent configuration.
The `sdp-pipelines` repository contains category registration pipelines and Geneva namespace configuration.

Use [deployment](deployment.md) for configuration changes.
Use [troubleshooting](troubleshooting.md) to inspect a running system.

## More documentation

See [Geneva Shoebox Logs documentation](https://eng.ms/docs/products/geneva/connectors/shoeboxlogs/summary.html) for the Shoebox connector reference.

See [sdp-pipelines/hcp/docs](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/docs) for additional HCP deployment documentation.

## Diagrams

Each diagram is an SVG file in [diagrams](diagrams/).
The Markdown documents include the SVG files as images.
A viewer does not need Mermaid support.
Each document also explains the diagram in text.
