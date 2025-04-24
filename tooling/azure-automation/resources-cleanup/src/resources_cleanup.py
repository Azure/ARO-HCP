#!/usr/bin/env python3

from typing import List
import datetime
import os
import json
import sys

from azure.identity import ManagedIdentityCredential
from azure.core.exceptions import HttpResponseError, ResourceNotFoundError
from azure.mgmt.resource import ResourceManagementClient
from azure.mgmt.resource.resources.v2022_09_01.models._models_py3 import GenericResourceExpanded, ResourceGroup

_KEY_NAME = "Name"
_KEY_VALUE = "Value"
_KEY_VARIABLE = "Variable"
_KEY_CREDENTIAL = "Credential"


def _get_automation_asset_file():
    if os.environ.get('AUTOMATION_ASSET_FILE') is not None:
        return os.environ.get('AUTOMATION_ASSET_FILE')
    return os.path.join(os.path.dirname(__file__), "localassets.json")

def _get_asset_value(asset_file, asset_type, asset_name):
    try:
        with open(asset_file) as json_data:
            local_assets = json.load(json_data)
    except (FileNotFoundError, json.JSONDecodeError):
        raise LookupError(f"Asset file not found or invalid: {asset_file}")

    return_value = None
    for asset, asset_values in local_assets.items():
        if asset == asset_type:
            for value in asset_values:
                if value[_KEY_NAME] == asset_name:
                    return_value = value
                    break
        if return_value is not None:
            break

    return return_value

def _get_asset(asset_type, asset_name):
    local_assets_file = _get_automation_asset_file()
    return_value = _get_asset_value(local_assets_file, asset_type, asset_name)

    if return_value is None:
        raise LookupError(f"Asset '{asset_name}' not found")
    return return_value

def get_automation_variable(name):
    """ Returns an automation variable """
    variable = _get_asset(_KEY_VARIABLE, name)
    return variable[_KEY_VALUE]

def get_automation_credential(name):
    """ Returns an automation credential as a dictionary with username and password as keys """
    credential = _get_asset(_KEY_CREDENTIAL, name)

    # Return a dictionary of the credential asset
    credential_dictionary = {}
    credential_dictionary['username'] = credential['Username']
    credential_dictionary['password'] = credential['Password']
    return credential_dictionary

# If DRY_RUN is TRUE, the script will print which resource groups should be deleted
# without deleting them. If it is FALSE, the script will print which resource groups
# should be deleted and delete those that should be deleted.
DRY_RUN = False

# VERBOSE is used to control whether to print all the resources of each resource group
# for informational purposes.
VERBOSE = False

DEFAULT_API_VERSION = "2022-09-01"


def get_date_time_from_str(date_time_str: str) -> datetime.datetime:
    """ get_date_time_from_str expects an input date in ISO 8601 with Z suffix
    e,g: 2024-01-26T17:08:13.8139962Z
    In Python < 3.11, fromisoformat() does not accept Z suffix even if it is valid in
    ISO 8601 (https://discuss.python.org/t/parse-z-timezone-suffix-in-datetime/2220).
    """
    date_time_str = date_time_str.replace("Z", "").replace("z", "")
    dot = "."
    if dot in date_time_str:
        dot_index = date_time_str.index(dot)
        date_time_str = date_time_str[0:dot_index]

    utc_suffix = '+00:00'
    date_time_str += utc_suffix

    return datetime.datetime.fromisoformat(date_time_str)


def time_delta_greater_than_two_days(now: datetime.datetime, resource_group_creation_time: datetime.datetime):
    if now is None:
        print("now time is None")
        return False

    if resource_group_creation_time is None:
        print("resource_group_creation_time is None")
        return False

    time_delta = resource_group_creation_time - now
    n_days = abs(time_delta.days)

    return n_days > 2

def print_resources(resource_list: List[GenericResourceExpanded]):
    for resource in resource_list:
        print(f"- name: {resource.name}")
        print(f"    ID: {resource.id}")
        print(f"    type: {resource.type}")
        print(f"    created at: {resource.created_time}")
        print(f"    changed at: {resource.changed_time}")
        print(f"    tags: {resource.tags}\n")


def resource_group_has_persist_tag_as_true(resource_group: ResourceGroup):
    if resource_group.tags is None:
        return False

    persist_tag = "persist"
    if persist_tag not in resource_group.tags:
        return False

    return resource_group.tags[persist_tag].lower() == "true"

def resource_group_is_managed(resource_group: ResourceGroup):
    return resource_group.managed_by is not None

def process_resource_groups_of_subscription(subscription_id: str, resource_client: ResourceManagementClient):
    resource_groups_list = list(resource_client.resource_groups.list())
    n_resource_groups = len(resource_groups_list)
    print(f"subscription {subscription_id} has {n_resource_groups} resource groups:\n")

    for resource_group in resource_groups_list:
        try:
            process_resource_group(resource_group, resource_client)
        except ResourceNotFoundError as err:
            print(f"Encountered a missing resource (probably the rg itself).")
            print(f"This is fine, it must've gotten deleted by something else; continuing.")
            print(f"Code: {err.error.code}")
            print(f"Message: {err.error.message}")
        print("_"*80)
        print()


def process_resource_group(resource_group: ResourceGroup, resource_client: ResourceManagementClient):
    resource_group_name = resource_group.name

    print(f"Resource group '{resource_group_name}':")
    print(f"Managed by: {resource_group.managed_by}")
    print(f"Tags: {resource_group.tags}\n")

    if VERBOSE:
        resource_list = list(
            resource_client.resources.list_by_resource_group(resource_group_name, expand="createdTime,changedTime")
        )
        print(f"This resource group has {len(resource_list)} resources \n")
        print_resources(resource_list)

    if resource_group_has_persist_tag_as_true(resource_group):
        print(f"Persist tag is true, this resource group should NOT be deleted, skipping.")
        return

    if resource_group_is_managed(resource_group):
        print(f"Resource Group is managed, this resource group should NOT be deleted, skipping.")
        return

    now = datetime.datetime.now(datetime.timezone.utc)
    resource_group_creation_time = get_creation_time_of_resource_group(resource_group)
    if not time_delta_greater_than_two_days(now, resource_group_creation_time):
        print(f"This resource group should NOT be deleted, it is not older than two days, skipping.")
        return

    print("This resource group should be deleted.\n")
    if DRY_RUN:
        return

    try:
        print("\nBeginning deletion of this resource group ...")
        result_poller = resource_client.resource_groups.begin_delete(resource_group_name)
        print(f"result_poller of resource group deletion: {result_poller}")
    except HttpResponseError as err:
        error_codes = ("DenyAssignmentAuthorizationFailed", "ScopeLocked")
        if err.error.code in error_codes:
            print(f"skipping deletion of resource group due to error code {err.error.code}")
        else:
            raise err

def get_creation_time_of_resource_group(resource_group):
    resource_group_creation_time = None
    created_at_tag = "createdAt"
    if resource_group.tags is not None and created_at_tag in resource_group.tags:
        resource_group_creation_time = get_date_time_from_str(resource_group.tags[created_at_tag])
    return resource_group_creation_time


# https://learn.microsoft.com/en-us/azure/automation/shared-resources/variables?tabs=azure-powershell#python-functions-to-access-variables
def get_subscription_id():
    try:
        return get_automation_variable("subscription_id")
    except LookupError:
        env_val = os.getenv("SUBSCRIPTION_ID")
        if env_val:
            return env_val
        raise ValueError(
            "Subscription ID missing: not found in automation variables or SUBSCRIPTION_ID env var."
        )

def main():
    subscription_id = sys.argv[1]
    if not subscription_id:
        raise ValueError("Subscription ID not found in automation variables or environment variables")

    resource_client = ResourceManagementClient(
        credential=ManagedIdentityCredential(client_id=sys.argv[2]),
        subscription_id=subscription_id,
        api_version=DEFAULT_API_VERSION
    )

    runbook_name = 'Deletion Runbook'
    print(f"'{runbook_name} started'\n")

    print(f"DRY_RUN flag is {DRY_RUN}\n")
    print(f"VERBOSE flag is {VERBOSE}\n")

    process_resource_groups_of_subscription(subscription_id, resource_client)
    print(f"\n'{runbook_name}' finished")

if __name__ == "__main__":
    main()
