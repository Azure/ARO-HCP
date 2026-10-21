#!/usr/bin/python3

import argparse
import json
import os

import yaml
from yaml.resolver import BaseResolver

# This script updates the available SKUs (instance-types) for node pools in the configmap used by ClustersService.
# It requires an active az session, pointing to the right subscription by default.
#
# Examples:
# $ python update_instance_types.py
# $ python update_instance_types.py --help
# $ python update_instance_types.py --region eastus --output cloud-resources-config.yaml

parser = argparse.ArgumentParser(description='Generates a list of available instance types in the given region. '
                                             'Requires an active az session to run "az vm list-sizes" on the shell.')
parser.add_argument('--region', default="westus3", help='the azure region, by default westus3')
parser.add_argument('--input', default="deploy/templates/cloud-resources-config.configmap.yaml",
                    help='the input configmap, by default the cloud resources-config in the helm template directory')
parser.add_argument('--output', default="deploy/templates/cloud-resources-config.configmap.yaml",
                    help='the output configmap, by default the cloud resources-config in the helm template directory')
args = parser.parse_args()


class AsLiteral(str):
    pass


def represent_literal(dumper, data):
    return dumper.represent_scalar(BaseResolver.DEFAULT_SCALAR_TAG, data, style="|")


yaml.add_representer(AsLiteral, represent_literal)

if not os.path.exists(args.input):
    raise FileNotFoundError(f"Input file not found: {args.input}")

with open(args.input) as f:
    try:
        cm_content = yaml.safe_load(f)
    except yaml.YAMLError as e:
        raise RuntimeError(f"Error parsing input YAML file {args.input}: {e}")

# shelling out here to avoid importing all kinds of Azure libraries via pip
json_out = os.popen(f"az vm list-sizes --location \"{args.region}\"").read()
if not json_out.strip():
    raise RuntimeError("Unable to list vm sizes. Please check that an azure session is active and is able to run az commands.")
types = json.loads(json_out)

instance_types_set = {}
instance_types_list = []
for i in range(len(types)):
    vm_type = types[i]
    type_name = vm_type['name']
    print(f"processing {vm_type['name']}")
    instance_type = {
        "cloud_provider_id": "azure",
        "id": type_name,
        "name": type_name,
        "generic_name": type_name.lower(),
        "cpu_cores": int(vm_type["numberOfCores"]),
        "memory": int(vm_type["memoryInMB"]) * 1024 * 1024,
    }

    if "L" in type_name:
        instance_type["category"] = "storage_optimized"
    elif "N" in type_name:
        instance_type["category"] = "gpu_workload"
    elif "E" in type_name:
        instance_type["category"] = "memory_optimized"
    elif "F" in type_name:
        instance_type["category"] = "compute_optimized"
    elif "D" in type_name:
        instance_type["category"] = "general_purpose"
    else:
        print(f"skipping unknown {type_name}")
        continue

    if "Promo" in type_name:
        print(f"skipping Promo type: {type_name}")
        continue

    if "p" in type_name:
        instance_type["architecture"] = "arm64"

    if type_name not in instance_types_set:
        instance_types_list.append(instance_type)
        instance_types_set[type_name] = True

# we need to double-yaml-dump because the configmap stores the file as a yaml inside the yaml
cm_content["data"]["instance-types.yaml"] = AsLiteral(yaml.dump({"instance_types": instance_types_list}))
cm_content["data"]["cloud-regions.yaml"] = AsLiteral(cm_content["data"]["cloud-regions.yaml"])
with open(args.output, "w") as f:
    yaml.dump(cm_content, f)
