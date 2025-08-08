# Azure Public IP Address Listing Scripts

This directory contains scripts to connect to Azure and list all public IP addresses grouped by subscription, region, and IP tags with counts.

## Python Version

### Setup

1. Create and activate virtual environment:
```bash
python3 -m venv venv
source venv/bin/activate  # On Windows: venv\Scripts\activate
```

2. Install dependencies:
```bash
pip install -r requirements.txt
```

### Authentication

Ensure you're authenticated with Azure:
```bash
az login
```

### Usage

```bash
# Basic usage
python list-ip-addresses.py

# Save results to file
OUTPUT_FILE=results.txt python list-ip-addresses.py
```

### Deactivate virtual environment

```bash
deactivate
```

## PowerShell Version

### Prerequisites

- PowerShell 7+ (pwsh)
- Azure PowerShell modules:
  ```powershell
  Install-Module -Name Az.Accounts, Az.Network, Az.Resources -Force
  ```

### Authentication

```powershell
Connect-AzAccount
```

### Usage

```powershell
# Basic usage
.\list-ip-addresses.ps1

# Save results to file
.\list-ip-addresses.ps1 -OutputFile "results.txt"
```

## Output Format

Both scripts output data in Prometheus metric format:

```
azure_public_ip_tag_count{subscription="subscription-id",region="region-name",ipTagType="tag-type",tag="tag-value"} count
```