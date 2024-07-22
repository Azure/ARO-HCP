#!/bin/bash

CONSUMER_NAME=$1

# Start kubectl port-forward in the background
kubectl port-forward svc/maestro 8001:8000 -n maestro &
PORT_FORWARD_PID=$!

# Wait a bit to ensure the port-forward is established
sleep 5

cat << EOF | curl -X POST -H "Content-Type: application/json" -d @- http://localhost:8001/api/maestro/v1/consumers
{
  "name": "$CONSUMER_NAME"
}
EOF

# Clean up: Kill the port-forward process
kill $PORT_FORWARD_PID
