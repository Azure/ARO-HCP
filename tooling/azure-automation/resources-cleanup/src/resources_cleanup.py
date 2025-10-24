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

from automationassets import get_automation_variable

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

def time_delta_greater_than_one_month(now: datetime.datetime, resource_group_creation_time: datetime.datetime):
    if now is None:
        print("now time is None")
        return False

    if resource_group_creation_time is None:
        print("resource_group_creation_time is None")
        return False

    time_delta = resource_group_creation_time - now
    n_days = abs(time_delta.days)

    return n_days > 30

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

    # Special handling for hcp-underlay-pers-* resource groups
    if resource_group_name.startswith("hcp-underlay-pers-"):
        now = datetime.datetime.now(datetime.timezone.utc)
        resource_group_creation_time = get_creation_time_of_resource_group(resource_group)

        if resource_group_creation_time is None:
            print(f"Resource group '{resource_group_name}' has no createdAt tag, skipping deletion for safety.")
            return

        if not time_delta_greater_than_one_month(now, resource_group_creation_time):
            print(f"Personal development environment resource group '{resource_group_name}' is not older than one month, skipping.")
            return

        print(f"Personal development environment resource group '{resource_group_name}' is older than one month and should be deleted.\n")
        if DRY_RUN:
            return

        try:
            print(f"\nBeginning deletion of personal development environment resource group '{resource_group_name}' ...")
            result_poller = resource_client.resource_groups.begin_delete(resource_group_name)
            print(f"result_poller of resource group deletion: {result_poller}")
        except HttpResponseError as err:
            error_codes = ("DenyAssignmentAuthorizationFailed", "ScopeLocked")
            if err.error.code in error_codes:
                print(f"skipping deletion of resource group due to error code {err.error.code}")
            else:
                raise err
        return

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
    except:
        env_val = os.getenv("SUBSCRIPTION_ID")
        if env_val:
            return env_val
        raise ValueError(
            "Subscription ID missing: not found in automation variables or SUBSCRIPTION_ID env var."
        )

def get_client_id():
    try:
        return get_automation_variable("client_id")
    except:
        env_val = os.getenv("CLIENT_ID")
        if env_val:
            return env_val
        raise ValueError(
            "Client ID missing: not found in automation variables or CLIENT_ID env var."
        )

def get_boolean_from_string(val):
    """
    Convert a string representation of truth to True or False.

    True values are 'y', 'yes', 't', 'true', 'on', and '1';
    False values are 'n', 'no', 'f', 'false', 'off', and '0'.
    Raises ValueError if 'val' is anything else.

    Args:
        val (str): The string to convert.

    Returns:
        bool: The boolean value corresponding to the string.

    Raises:
        ValueError: If the string does not represent a boolean value.
    """
    if not isinstance(val, str):
        raise ValueError(f"Invalid truth value: {val!r} (type: {type(val).__name__}). Expected a string.")
    val_stripped = val.strip().lower()
    true_set = {'y', 'yes', 't', 'true', 'on', '1'}
    false_set = {'n', 'no', 'f', 'false', 'off', '0'}
    if val_stripped in true_set:
        return True
    if val_stripped in false_set:
        return False
    raise ValueError(
        f"Invalid truth value: {val!r}. "
        f"Expected one of {sorted(true_set | false_set)} (case-insensitive, whitespace ignored)."
    )

def get_dry_run():
    """
    Retrieve the dry run flag from automation variables or environment variable.

    Returns:
        bool: True if dry run is enabled, False otherwise.
    """
    try:
        val = get_automation_variable("dry_run")
        return get_boolean_from_string(val)
    except Exception:
        env_val = os.getenv("DRY_RUN")
        if env_val is not None:
            try:
                return get_boolean_from_string(env_val)
            except ValueError as e:
                print(f"Warning: Invalid DRY_RUN environment variable value: {env_val!r}. Defaulting to False.")
                return False
        print("Info: DRY_RUN not set in automation variables or environment. Defaulting to False.")
        return False

# If DRY_RUN is TRUE, the script will print which resource groups should be deleted
# without deleting them. If it is FALSE, the script will print which resource groups
# should be deleted and delete those that should be deleted.
DRY_RUN = get_dry_run()

def main():

    resource_client = ResourceManagementClient(
        credential=ManagedIdentityCredential(client_id=get_client_id()),
        subscription_id=get_subscription_id(),
        api_version=DEFAULT_API_VERSION
    )

    runbook_name = 'Deletion Runbook'
    print(f"'{runbook_name} started'\n")

    print(f"DRY_RUN flag is {DRY_RUN}\n")
    print(f"VERBOSE flag is {VERBOSE}\n")

    process_resource_groups_of_subscription(get_subscription_id(), resource_client)
    print(f"\n'{runbook_name}' finished")

if __name__ == "__main__":
    main()
