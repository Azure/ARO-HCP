using System.Net.Http.Headers;
using System.Text.Json;
using Azure.Core;
using Azure.Identity;
using GenevaHealthFunctionSample.Configuration;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

namespace GenevaHealthFunctionSample.Services;

public sealed class PrometheusQueryService
{
    private static readonly string[] PrometheusScopes = ["https://prometheus.monitor.azure.com/.default"];

    private readonly HttpClient _httpClient;
    private readonly DefaultAzureCredential _credential;
    private readonly AmwOptions _options;
    private readonly ILogger<PrometheusQueryService> _logger;

    public PrometheusQueryService(
        HttpClient httpClient,
        IOptions<AmwOptions> options,
        ILogger<PrometheusQueryService> logger)
    {
        _httpClient = httpClient;
        _credential = new DefaultAzureCredential();
        _options = options.Value;
        _logger = logger;
    }

    public async Task<PrometheusQueryResult> QueryAsync(string promql, CancellationToken cancellationToken = default)
    {
        var token = await _credential.GetTokenAsync(
            new TokenRequestContext(PrometheusScopes),
            cancellationToken);

        var endpoint = _options.PrometheusQueryEndpoint.TrimEnd('/');
        var requestUrl = $"{endpoint}/api/v1/query?query={Uri.EscapeDataString(promql)}";

        using var request = new HttpRequestMessage(HttpMethod.Get, requestUrl);
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token.Token);

        _logger.LogInformation("Querying Prometheus: {Query}", promql);

        using var cts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        cts.CancelAfter(TimeSpan.FromSeconds(_options.QueryTimeoutSeconds));

        using var response = await _httpClient.SendAsync(request, cts.Token);
        response.EnsureSuccessStatusCode();

        var json = await response.Content.ReadAsStringAsync(cts.Token);
        var result = JsonSerializer.Deserialize<PrometheusResponse>(json, JsonOptions);

        if (result is null || result.Status != "success")
        {
            throw new InvalidOperationException(
                $"Prometheus query failed. Status: {result?.Status}, Error: {result?.Error}");
        }

        return result.Data ?? new PrometheusQueryResult();
    }

    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNameCaseInsensitive = true
    };
}

public sealed class PrometheusResponse
{
    public string Status { get; init; } = string.Empty;
    public string? Error { get; init; }
    public PrometheusQueryResult? Data { get; init; }
}

public sealed class PrometheusQueryResult
{
    public string ResultType { get; init; } = string.Empty;
    public List<PrometheusVectorResult> Result { get; init; } = [];
}

public sealed class PrometheusVectorResult
{
    public Dictionary<string, string> Metric { get; init; } = new();
    public JsonElement[]? Value { get; init; }
}
