using GenevaHealthFunctionSample.Configuration;
using GenevaHealthFunctionSample.Services;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

var host = new HostBuilder()
    .ConfigureFunctionsWorkerDefaults()
    .ConfigureServices((context, services) =>
    {
        services
            .AddOptions<GenevaOptions>()
            .Bind(context.Configuration.GetSection("Geneva"));

        services
            .AddOptions<AmwOptions>()
            .Bind(context.Configuration.GetSection("Amw"));

        // TODO: re-enable once Microsoft.Cloud.HealthService.Client is accessible
        // services.AddSingleton<GenevaHealthClientFactory>();
        services.AddHttpClient<PrometheusQueryService>();
    })
    .Build();

host.Run();
