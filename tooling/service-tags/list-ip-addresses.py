#!/usr/bin/env python3
"""
Script to connect to Azure and list all public IP addresses.
"""

import argparse
import csv
import json
import os
import sys
import time
from datetime import datetime, timezone
from azure.identity import DefaultAzureCredential
from azure.mgmt.network import NetworkManagementClient
from azure.mgmt.resource import ResourceManagementClient
from azure.mgmt.subscription import SubscriptionClient
from azure.monitor.ingestion import LogsIngestionClient


def get_all_subscriptions(credential):
    """Get all accessible subscriptions."""
    subscription_client = SubscriptionClient(credential)
    return [sub for sub in subscription_client.subscriptions.list()]


def send_metrics_to_workspace(credential, workspace_endpoint, rule_id, stream_name, grouped_data):
    """Send metrics to Azure Monitor Workspace."""
    try:
        client = LogsIngestionClient(endpoint=workspace_endpoint, credential=credential)
        
        # Convert grouped data to log entries
        log_entries = []
        for (subscription_id, region, ip_tag_type, tag_value), ip_addresses in grouped_data.items():
            ip_count = len([ip for ip in ip_addresses if ip])
            log_entry = {
                "TimeGenerated": datetime.now(timezone.utc).isoformat(),
                "SubscriptionId": subscription_id,
                "Region": region,
                "IpTagType": ip_tag_type,
                "TagValue": tag_value,
                "IpCount": ip_count,
                "MetricName": "azure_public_ip_tag_count"
            }
            log_entries.append(log_entry)
        
        if log_entries:
            client.upload(rule_id=rule_id, stream_name=stream_name, logs=log_entries)
            print(f"Successfully sent {len(log_entries)} metric entries to Azure Workspace", file=sys.stderr)
        else:
            print("No metrics to send to Azure Workspace", file=sys.stderr)
            
    except Exception as e:
        print(f"Error sending metrics to Azure Workspace: {e}", file=sys.stderr)


def list_public_ips_in_subscription(credential, subscription_id):
    """List all public IP addresses in a subscription."""
    network_client = NetworkManagementClient(credential, subscription_id)
    public_ips = []

    try:
        for pip in network_client.public_ip_addresses.list_all():
            ip_info = {
                'subscription_id': subscription_id,
                'resource_group': pip.id.split('/')[4],
                'name': pip.name,
                'ip_address': pip.ip_address,
                'location': pip.location,
                'provisioning_state': pip.provisioning_state,
                'iptags': pip.ip_tags
            }


            public_ips.append(ip_info)
    except Exception as e:
        print(f"Error listing public IPs in subscription {subscription_id}: {e}")

    return public_ips


def parse_arguments():
    """Parse command line arguments."""
    parser = argparse.ArgumentParser(description="List Azure public IP addresses and optionally send metrics to Azure Workspace")
    parser.add_argument("--workspace-endpoint", 
                        help="Azure Monitor Data Collection Endpoint URL (e.g., https://myendpoint-abc.eastus-1.ingest.monitor.azure.com)")
    parser.add_argument("--rule-id", 
                        help="Data Collection Rule ID for Azure Monitor")
    parser.add_argument("--stream-name", 
                        help="Data stream name for Azure Monitor")
    return parser.parse_args()


def main():
    """Main function to list all public IP addresses."""
    try:
        # Parse command line arguments
        args = parse_arguments()
        
        # Initialize Azure credential
        credential = DefaultAzureCredential()

        # Get all subscriptions
        subscriptions = get_all_subscriptions(credential)

        if not subscriptions:
            print("No accessible subscriptions found.")
            return

        all_public_ips = []

        for subscription in subscriptions:
            #print(f"Checking subscription: {subscription.display_name} ({subscription.subscription_id})", file=sys.stderr)

            public_ips = list_public_ips_in_subscription(credential, subscription.subscription_id)
            all_public_ips.extend(public_ips)

        # Group by subscription, region, and ip_tag
        if all_public_ips:
            grouped_data = {}

            for ip in all_public_ips:
                subscription_id = ip['subscription_id']
                location = ip['location']
                ip_address = ip['ip_address']

                if ip['iptags']:
                    for tag in ip['iptags']:
                        key = (subscription_id, location, tag.ip_tag_type, tag.tag)

                        if key not in grouped_data:
                            grouped_data[key] = []
                        grouped_data[key].append(ip_address)
                else:
                    # Handle IPs with no tags
                    key = (subscription_id, location, "None", "None")
                    if key not in grouped_data:
                        grouped_data[key] = []
                    grouped_data[key].append(ip_address)

            # Output in Prometheus format
            for (subscription_id, region, ip_tag_type, tag_value), ip_addresses in sorted(grouped_data.items()):
                ip_count = len([ip for ip in ip_addresses if ip])
                print(f'azure_public_ip_tag_count{{subscription="{subscription_id}",region="{region}",ipTagType="{ip_tag_type}",tag="{tag_value}"}} {ip_count}')

            # Optionally save to file
            output_file = os.environ.get('OUTPUT_FILE')
            if output_file:
                with open(output_file, 'w') as f:
                    for (subscription_id, region, ip_tag_type, tag_value), ip_addresses in sorted(grouped_data.items()):
                        ip_count = len([ip for ip in ip_addresses if ip])
                        f.write(f'azure_public_ip_tag_count{{subscription="{subscription_id}",region="{region}",ipTagType="{ip_tag_type}",tag="{tag_value}"}} {ip_count}\n')
                print(f"\nResults also saved to: {output_file}", file=sys.stderr)
            
            # Send metrics to Azure Workspace if configured
            if args.workspace_endpoint and args.rule_id and args.stream_name:
                send_metrics_to_workspace(credential, args.workspace_endpoint, args.rule_id, args.stream_name, grouped_data)
            elif any([args.workspace_endpoint, args.rule_id, args.stream_name]):
                print("Warning: To send metrics to Azure Workspace, all three parameters are required: --workspace-endpoint, --rule-id, and --stream-name", file=sys.stderr)
        else:
            print("No public IP addresses found.", file=sys.stderr)

    except Exception as e:
        print(f"Error: {e}")
        return 1

    return 0


if __name__ == "__main__":
    exit(main())