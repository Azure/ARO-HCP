using GenevaHealthFunctionSample.Configuration;
using GenevaHealthFunctionSample.Services;
using Microsoft.Azure.Functions.Worker;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

namespace GenevaHealthFunctionSample.Functions;

public sealed class KubeNodeHealthFunction
{
    private readonly PrometheusQueryService _prometheus;
    private readonly GenevaOptions _options;
    private readonly ILogger<KubeNodeHealthFunction> _logger;

    public KubeNodeHealthFunction(
        PrometheusQueryService prometheus,
        IOptions<GenevaOptions> options,
        ILogger<KubeNodeHealthFunction> logger)
    {
        _prometheus = prometheus;
        _options = options.Value;
        _logger = logger;
    }

    [Function("KubeNodeHealth")]
    public async Task Run(
        [TimerTrigger("0 0 * * * *")] TimerInfo timer,
        CancellationToken cancellationToken)
    {
        _logger.LogInformation("KubeNodeHealth timer triggered at {Time}", DateTime.UtcNow);

        var nodeInfoResult = await _prometheus.QueryAsync("kube_node_info", cancellationToken);
        var nodeReadyResult = await _prometheus.QueryAsync(
            "kube_node_status_condition{condition=\"Ready\",status=\"true\"}", cancellationToken);

        var readyNodes = new HashSet<string>(
            nodeReadyResult.Result
                .Where(r => r.Metric.ContainsKey("node"))
                .Select(r => r.Metric["node"]));

        var healthyCount = 0;
        var unhealthyCount = 0;

        foreach (var nodeResult in nodeInfoResult.Result)
        {
            if (!nodeResult.Metric.TryGetValue("node", out var nodeName))
            {
                continue;
            }

            var isReady = readyNodes.Contains(nodeName);
            var status = isReady ? "Healthy" : "Error";
            var message = isReady
                ? $"Node {nodeName} is Ready"
                : $"Node {nodeName} is NotReady";

            if (isReady) healthyCount++; else unhealthyCount++;

            _logger.LogInformation(
                "Node {NodeName}: {Status} (Region: {Region})",
                nodeName, status, _options.DefaultRegion);

            // TODO: re-enable Geneva Health submission once Microsoft.Cloud.HealthService.Client is accessible
            // Build WatchdogReport and submit via GenevaHealthClientFactory
        }

        if (healthyCount == 0 && unhealthyCount == 0)
        {
            _logger.LogWarning("No nodes found in Prometheus query results.");
            return;
        }

        _logger.LogInformation(
            "KubeNodeHealth check complete. Healthy: {Healthy}, Unhealthy: {Unhealthy}",
            healthyCount, unhealthyCount);
    }
}
