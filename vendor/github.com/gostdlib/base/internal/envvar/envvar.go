// Package envvar holds the environment variables used by all base/ packages.
package envvar

const (
	// TracingEndpoint is the environment variable that contains the OTLP endpoint to use
	// for open telemetry tracing. If set, base/ will attempt to setup tracing to this endpoint.
	TracingEndpoint = "TracingEndpoint"
)
