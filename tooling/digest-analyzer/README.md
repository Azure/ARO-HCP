# Digest Analyzer

A CLI tool that analyzes ARO-HCP component image digests across environments, providing deployment tracking with git commit information and multiple output formats.

## Installation

```bash
# Build the application
go build

# Or use make if available
make build
```

## Usage

```bash
# Basic usage
./digest-analyzer <config-directory>

# With options
./digest-analyzer [flags] <config-directory>
```

## Command Line Options

### Output Formats

The tool supports two orthogonal formatting dimensions:

**Data Structure** (mutually exclusive):
- `wide` - Environments as columns (fewer rows, more columns)
- `narrow` - Compact 5-column layout (ENV, IMAGE, AGE, DIGEST, REV)
- (default) - Standard 7-column layout (COMPONENT, CLOUD, ENV, IMAGE, AGE, DIGEST, REV)

**Output Format** (mutually exclusive):
- `table` (default) - Tab-separated table format
- `md` - GitHub-formatted markdown tables with clickable links
- `gs` - CSV format with Excel hyperlinks

### Filter Options

- `-c, --component <string>` - Filter by component name (case-insensitive partial match)
- `-e, --envs <string>` - Comma-separated list of environments (e.g., `dev,int,stg`)

### Examples

```bash
# Default output (standard table format)
./digest-analyzer config

# Narrow layout with markdown formatting
./digest-analyzer -o narrow,md config

# Wide layout with CSV output
./digest-analyzer -o wide,gs config

# Filter by component and environment
./digest-analyzer -c "frontend" -e "dev,int" config

# Multiple filters with custom output
./digest-analyzer -o md,narrow -c "maestro" -e "int,prod" config
```

## Valid Output Format Combinations

| Data Structure | Output Format | Example Command |
|----------------|---------------|-----------------|
| (standard) | `table` | `./digest-analyzer config` |
| (standard) | `md` | `./digest-analyzer -o md config` |
| (standard) | `gs` | `./digest-analyzer -o gs config` |
| `narrow` | `table` | `./digest-analyzer -o narrow config` |
| `narrow` | `md` | `./digest-analyzer -o narrow,md config` |
| `narrow` | `gs` | `./digest-analyzer -o narrow,gs config` |
| `wide` | `table` | `./digest-analyzer -o wide config` |
| `wide` | `md` | `./digest-analyzer -o wide,md config` |
| `wide` | `gs` | `./digest-analyzer -o wide,gs config` |

## Features

- **Multi-environment support**: Analyzes dev, cspr, int, stg, and prod environments
- **Git integration**: Shows commit information and relative age for each digest
- **Component filtering**: Case-insensitive partial matching for component names
- **Environment filtering**: Select specific environments to display
- **Multiple output formats**: Table, Markdown, and CSV with proper formatting
- **Flexible layouts**: Standard, narrow, and wide data structures
- **Source tracking**: Identifies which configuration file contains each digest

## Supported Environments

The tool analyzes the following environments with proper configuration precedence:

1. **dev** (cloud: dev, env: dev)
2. **cspr** (cloud: dev, env: cspr)
3. **int** (cloud: public, env: int)
4. **stg** (cloud: public, env: stg)
5. **prod** (cloud: public, env: prod)

## Component Categories

Components are automatically categorized and sorted:

_These might use some modifications --mmazur_

1. **ACM** - Advanced Cluster Management
2. **Maestro** - Multi-cluster orchestration
3. **Frontend** - Resource Provider API frontend
4. **Backend** - Resource Provider backend processing
5. **HyperShift** - Hosted control plane operator
6. **PKO** - Package Operator
7. **Cluster Service** - Core cluster management
8. **Image Sync** - Container image synchronization
9. **Monitoring** - Prometheus and monitoring stack
10. **ACR Pull** - Azure Container Registry access
11. **Secret Sync Controller** - Secret management
12. **Backplane API** - Cluster access API
13. **Logging** - Log forwarding and processing

## Sample Output

_If you end up using this, tell me how. Defaults can be changed. --mmazur_

### Standard Table Format
```
COMPONENT  CLOUD   ENV   IMAGE     AGE  DIGEST                                                            REV
Frontend   public  int   Frontend  7d   2d40f3eb66bf0fb8c696641b39aba2ed8105807df61c53778f6fb151567266c5  https://github.com/Azure/ARO-HCP/commit/ff067930aa
```

### Narrow Markdown Format
```markdown
| ENV | IMAGE | AGE | DIGEST | REV |
|-----|-------|-----|--------|-----|
| int | Frontend | 7d | 2d40f3eb66 | [ff067930aa](https://github.com/Azure/ARO-HCP/commit/ff067930aa) |
```

### Wide CSV Format
```csv
Component,Image,public.int Digest,public.int Age,public.stg Digest,public.stg Age
Frontend,Frontend,"=HYPERLINK(""https://github.com/Azure/ARO-HCP/commit/ff067930aa"",""2d40f3eb66"")",7d,"=HYPERLINK(""https://github.com/Azure/ARO-HCP/commit/b842e7ed6a"",""84efda0d58"")",11d
```

