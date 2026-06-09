# prometheus-rules

A CLI tool for generating Azure Monitor Prometheus rule groups from Prometheus Operator rule definitions.

## Overview

This tool converts Kubernetes PrometheusRule custom resources (used by the Prometheus Operator) into Azure Bicep templates that deploy `Microsoft.AlertsManagement/prometheusRuleGroups` resources. It supports both alerting rules and recording rules, and includes built-in testing to ensure rule correctness.

## Features

- Converts PrometheusRule CRDs to Azure Bicep templates
- Supports both alerting rules and recording rules (generated into separate files)
- Validates rules using `promtool test rules`
- Maps Prometheus severity labels to IcM (Incident Management) severity levels
- Automatically generates IcM correlation IDs for proper incident aggregation
- Supports expression replacements for platform-specific adjustments

## Usage

### Build

```bash
make
```

### Run

```bash
# Generate everything (alerts + recording rules)
make run

# Generate all alerts or all recording rules
make alerts          # All 4 alert configs
make recording-rules # Both recording-rules configs

# Generate individually
make run-sl-services              # Alerting rules: SL queue, services datasource
make run-sre-hcps                 # Alerting rules: SRE queue, HCPs datasource
make run-rp-services              # Alerting rules: RP queue, services datasource
make run-msft-services            # Alerting rules: MSFT queue, services datasource
make run-recording-rules-services # Recording rules: services datasource
make run-recording-rules-hcps    # Recording rules: HCPs datasource

# Custom configuration
go run . --config-file path/to/config.yaml
```

Note: `run`, `alerts`, and `recording-rules` automatically run `fmt-devinfra` after generation. Individual `run-*` targets do not.

### Command-line Options

- `--config-file` (required): Path to configuration YAML file
- `--force-info-severity`: Override all alert severities to "info" level (useful for testing)

## Configuration

The tool expects a YAML configuration file with the following structure:

```yaml
prometheusRules:
  # Directories containing rule files (each must have a corresponding _test file)
  rulesFolders:
    - path/to/rules

  # Rule files without tests (not recommended)
  untestedRules:
    - path/to/untested/rules.yaml

  # Output Bicep file path
  outputBicep: path/to/output.bicep

  # Default evaluation interval for rule groups (e.g., "1m")
  defaultEvaluationInterval: "1m"

  # Expression replacements (for platform-specific adjustments)
  outputReplacements:
    - from: 'original_expression'
      to: 'replaced_expression'
```

## Rule Testing

All rules in `rulesFolders` **must** have corresponding test files:
- Rule file: `alerts.yaml`
- Test file: `alerts_test.yaml`

Tests are executed using `promtool test rules` during the generation process. If any test fails, the generation will abort.

## Severity Mapping

The tool maps Prometheus severity labels to Azure Monitor/IcM severity levels:

| Prometheus Severity | Description               | IcM Severity | IcM Severity Description                    |
|---------------------|---------------------------|--------------|---------------------------------------------|
| Critical            | Important component       | 2            | Single service SLA impact.                  |
| Warning             | Component degradation     | 3            | Urgent/high business impact, no SLA impact. |
| Info                | Something may be going on | 4            | Not urgent, no SLA impact.                  |

See: [IcM best practices - Severity levels](https://msazure.visualstudio.com/AzureRedHatOpenShift/_wiki/wikis/ARO.wiki/838022/IcM-best-practices?anchor=severity-levels)

## Output

The tool generates Azure Bicep templates with two different formats:

### Alerting Rules

- Output filename must contain `AlertingRules`
- Includes action group integrations for IcM
- Each alert includes:
  - Custom IcM title: `#{cluster}: {description}`
  - Correlation ID for proper incident aggregation
  - Severity mapping
  - All original labels and annotations

### Recording Rules

- Output filename must contain `RecordingRules`
- Simpler structure without alerting-specific features
- Used to pre-compute frequently-used queries

## Development

### Prerequisites

- Go 1.x+
- `promtool` (from Prometheus)

### Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...
```

### Project Structure

```
.
├── main.go              # CLI entry point
├── main_test.go         # CLI tests
├── internal/
│   ├── generator.go     # Core rule generation logic
│   ├── generator_test.go
│   ├── writer.go        # Expression replacement utilities
│   └── writer_test.go
└── README.md
```

## IcM Integration

The tool automatically configures IcM integration for alerting rules:

1. **Correlation ID**: Generated from alert name + cluster label + labels referenced in description
2. **Title**: Formatted as `cluster: description`
3. **Action Groups**: Referenced from Bicep parameters

For more information on IcM customization, see:
- [Customize ICM Fields](https://msazure.visualstudio.com/One/_git/EngSys-MDA-GenevaDocs?path=/documentation/alerts/HowDoI/CustomizeICMFields.md)
- [Prometheus IcM Connector Setup](https://dev.azure.com/msazure/One/_git/EngSys-MDA-GenevaDocs?path=/documentation/metrics/Prometheus/PromIcMConnectorsetup.md)
- [IcM Action Documentation](https://eng.ms/docs/products/icm/developers/connectors/icmaction#edit-an-azure-monitor-icm-connector-definition-icm-action)

## Known Limitations

- Query offsets are not supported (will generate a warning)
- Alert limits are not supported (will generate a warning)
- Minimum evaluation interval is 1 minute (shorter intervals will be adjusted)
