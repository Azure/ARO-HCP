# ARO dev observability

## Tracing

ARO frontend, cluster service and other components are instrumented with the OpenTelemetry SDK.
In the current development environment, there is no possibility to inspect traces.

### Deploy Jaeger all-in-one testing backend

#### Install
```
make deploy
```

After installation, the `jaeger` service becomes available in the `observability` namespace. We can access the UI using `kubectl port-forward`:

```
kubectl port-forward -n observability svc/jaeger 16686:16686
```

The `observability` namespace contains a second service named `ingest` which accepts otlp via gRPC and HTTP.

#### Configure instances

The export of the trace information is configured via environment variables. Existing deployments can be patched as follows:

```diff
+        env:
+        - name: OTEL_EXPORTER_OTLP_ENDPOINT
+          value: https://<service>.<namespace>:4318
+        - name: OTEL_TRACES_EXPORTER
+          value: otlp
```

You can use:

```
make patch-frontend
make patch-clusteservice
```


### Correlate with ARM requests

The following span attributes are set in the root span and propagated to the next service via baggage: 

```
aro.correlation.id
aro.request.id
aro.client.request.id
```
