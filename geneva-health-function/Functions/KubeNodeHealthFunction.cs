using GenevaHealthFunctionSample.Configuration;
using GenevaHealthFunctionSample.Services;
using Microsoft.Azure.Functions.Worker;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

namespace GenevaHealthFunctionSample.Functions;

internal sealed record NodeHealthInfo(
    string NodeName,
    string ResourceId,
    string Status,
    string Region,
    string HostedControlPlane);

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
        Console.WriteLine($"[KubeNodeHealth] Timer triggered at {DateTime.UtcNow:O}");
        _logger.LogInformation("KubeNodeHealth timer triggered at {Time}", DateTime.UtcNow);

        try
        {
            var nodeInfoResult = await _prometheus.QueryAsync("kube_node_info", cancellationToken);
            var nodeReadyResult = await _prometheus.QueryAsync(
                "kube_node_status_condition{condition=\"Ready\",status=\"true\"}", cancellationToken);

            // Build node → provider_id lookup from kube_node_info (distinct by node name)
            var nodeResourceIds = nodeInfoResult.Result
                .Where(r => r.Metric.ContainsKey("node"))
                .GroupBy(r => r.Metric["node"])
                .ToDictionary(
                    g => g.Key,
                    g => g.First().Metric.GetValueOrDefault("provider_id", ""));

            // Build node → {region, hostedcontrolplane} lookup from kube_node_status_condition
            var nodeConditions = nodeReadyResult.Result
                .Where(r => r.Metric.ContainsKey("node"))
                .GroupBy(r => r.Metric["node"])
                .ToDictionary(
                    g => g.Key,
                    g => (
                        Region: g.First().Metric.GetValueOrDefault("region", ""),
                        HCP: g.First().Metric.GetValueOrDefault("hostedcontrolplane", "")
                    ));

            var readyNodes = new HashSet<string>(nodeConditions.Keys);
            var allNodeNames = nodeResourceIds.Keys;

            var healthyCount = 0;
            var unhealthyCount = 0;

            foreach (var nodeName in allNodeNames)
            {
                var isReady = readyNodes.Contains(nodeName);
                var status = isReady ? "Healthy" : "Error";
                var resourceId = nodeResourceIds.GetValueOrDefault(nodeName, "");
                var region = nodeConditions.TryGetValue(nodeName, out var cond) ? cond.Region : "";
                var hcp = nodeConditions.TryGetValue(nodeName, out cond) ? cond.HCP : "";

                if (isReady) healthyCount++; else unhealthyCount++;

                Console.WriteLine($"[KubeNodeHealth] Node={nodeName}, Status={status}, Region={region}, HCP={hcp}, ResourceId={resourceId}");
                _logger.LogInformation(
                    "Node {NodeName}: Status={Status}, Region={Region}, HCP={HCP}, ResourceId={ResourceId}",
                    nodeName, status, region, hcp, resourceId);

                // TODO: re-enable Geneva Health submission once Microsoft.Cloud.HealthService.Client is accessible
            }

            if (healthyCount == 0 && unhealthyCount == 0)
            {
                Console.WriteLine("[KubeNodeHealth] WARNING: No nodes found in Prometheus query results.");
                _logger.LogWarning("No nodes found in Prometheus query results.");
                return;
            }

            Console.WriteLine($"[KubeNodeHealth] Complete. Healthy: {healthyCount}, Unhealthy: {unhealthyCount}");
            _logger.LogInformation(
                "KubeNodeHealth check complete. Healthy: {Healthy}, Unhealthy: {Unhealthy}",
                healthyCount, unhealthyCount);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[KubeNodeHealth] ERROR: {ex}");
            _logger.LogError(ex, "KubeNodeHealth check failed");
            throw;
        }
    }
}
