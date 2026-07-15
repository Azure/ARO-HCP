# hypershift / controlPlaneEvents

## Summary

Lists Kubernetes events from the hosted control plane namespace, excluding operator pods, to surface control plane component issues.

## What to Look For

Any anomalous events that indicate the components were crashing, failing health-checks, etc.

## Where to Go Next

Review logs for specific components if they were crashing to determine why.
