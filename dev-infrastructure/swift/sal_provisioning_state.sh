#!/bin/bash

source swift_env_vars

if ! is_redhat_user; then
    az login
fi

az network vnet subnet show -g $resource_group -n $subnet_name --vnet-name $vnet_name
