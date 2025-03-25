# Observability for developer environments

This page explains how you can enable tracing for ARO HCP in your [development setup](../dev-infrastructure/docs/development-setup.md).

## Pre-requisites

* `KUBECONFIG` environment variable set to the location of the Service Cluster's kubeconfig file.

## Tracing

The ARO frontend, cluster service and other components are instrumented with
the OpenTelemetry SDK but by default, there's no backend configured to collect
and visualize the traces.

### Deploy Jaeger all-in-one testing backend

We will deploy [Jaeger](https://www.jaegertracing.io/) with in-memory storage to store and visualize the traces received from the ARO-HCP components.

#### Install

```
make deploy
```

After installation, the `jaeger` service becomes available in the `observability` namespace. We can access the UI using `kubectl port-forward`:

```
kubectl port-forward -n observability svc/jaeger 16686:16686
```

Open http://localhost:16686 in your browser to access the Jaeger UI.
The `observability` namespace contains a second service named `ingest` which accepts otlp via gRPC and HTTP.

#### Configure the ARO services

Run the following commands:

```
make patch-frontend
make patch-clusterservice
```

The export of the trace information is configured via environment variables. Existing deployments are patched as follows:

```diff
+        env:
+        - name: OTEL_EXPORTER_OTLP_ENDPOINT
+          value: https://ingest.observability:4318
+        - name: OTEL_TRACES_EXPORTER
+          value: otlp
```

### Correlate with ARM requests

#### Generate Traces

Traces are automatically generated for every incoming HTTP request (sampling rate: 100%). A simple way to generate incoming requests is to follow the [demo instructions](../demo/README.md).

#### Common Attributes

A list of relevant span and resource attributes that are likely propagated to the next service via baggage can be found [here](tracing-common-attributes.md)
