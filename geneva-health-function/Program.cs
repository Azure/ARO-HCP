using GenevaHealthFunctionSample.Configuration;
using GenevaHealthFunctionSample.Services;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;

var host = new HostBuilder()
    .ConfigureFunctionsWorkerDefaults()
    .ConfigureLogging(logging =>
    {
        logging.AddConsole();
        logging.SetMinimumLevel(LogLevel.Information);
    })
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

Console.WriteLine("Geneva Health Function host starting...");
host.Run();
