#!/bin/bash

set -e

echo "=== CS Database Debug Commands ==="
echo ""

# Test database connectivity
echo "Testing database connectivity..."
if psql -c "SELECT 1" > /dev/null 2>&1; then
    echo "Database connection successful!"
else
    echo "Failed to connect to database"
    exit 1
fi

echo ""
provision_shard_id="1"
echo "List of clusters in provision shard '${provision_shard_id}'"
result=$(psql -tA -c "SELECT name FROM clusters where provision_shard_id = '${provision_shard_id}';")
if [ -z "$result" ]; then
    echo "No clusters present on provision_shard_id '${provision_shard_id}'"
else
    echo "Clusters present on provision_shard_id '${provision_shard_id}': $result"
fi

echo ""
echo "=== Debug commands completed ==="
