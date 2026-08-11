# Alert Verification

When you write an alert, you're usually not so sure about how it will behave with real production monitoring data.

With [alert-tester][alert-tester-repo] we have a fast approach to verify alerts with data from any environment, including PROD (in case the monitoring data is already there):

1. Come up with an alert
2. Analyze how it performs against historical data and tune it (a day)
3. Push to PROD (days to weeks)
4. Done

> [!IMPORTANT]
>
> All commands below need to be executed on a machine from which you can log in to your b- account!

> [!NOTE]
>
> In staging, integration, and production environments, Grafana may be restricted to the **MSFT Corp VPN**. If you cannot reach a non-dev Grafana instance, ensure you are connected to the VPN before further troubleshooting.

## Verifying alerts with [alert-tester][alert-tester-repo]

In a bash shell (on a machine from which you can log in to your b- account), e.g. WSL, Linux, or Git Bash:
```bash
git clone https://github.com/mmazur/alert-tester
cd alert-tester
make build
```

You can now use `./atest`, e.g. for PROD (replace `grafana-url` and `datasource` as desired for [other stages](#available-grafana-instances-and-data-sources)):

```bash
export ATEST_GRAFANA_BEARER_TOKEN=$(az account get-access-token \
      --resource ce34e7e5-485f-4d76-964f-b3d2b16d1e4f \
      --query accessToken -o tsv)

./atest grafana \
      --grafana-url GRAFANA_URL \
      --datasource services-australiaeast \
      -q 'time() - max without(prometheus_replica) (kube_lease_renew_time{namespace=~"^(kube-applier)$"})' \
      --gt 45,60,90,120,180 \
      --for 30s,1m,2m,3m \
      --from 2026-06-23 \
      --to 2026-07-09
```

This will give you a nice output like

```text
...

expr: time() - max without (prometheus_replica) (kube_lease_renew_time{namespace=~"^(kube-applier)$"})

local threshold > 180: 5549 samples pass
analysis:
- for 30s: 4 firings
- for 1m: 4 firings
- for 2m: 3 firings
- for 3m: 2 firings
```

For available Grafana URLs and datasources, see [Available Grafana Instances and Data Sources](#available-grafana-instances-and-data-sources). For more options and a detailed description, see [README.md][alert-tester-readme].

### AI-Generated Reports

The basic `atest` tool usage is already very helpful. But if you want to check multiple data sources and have a nice report, you might want to use the [aro-hcp-test-alerts skill][aro-hcp-test-alerts-skill]. To do so, you can:

1. Make sure you have [alert-tester][alert-tester-repo] cloned and built on a machine from which you can log in to your b- account (see above)
2. [Make sure you have Copilot properly set up][copilot-setup]
3. Have Copilot (or Claude with Copilot access) run from within the `alert-tester` dir
4. Ask Copilot something like
   ```text
   Run alert testing for PR https://github.com/Azure/ARO-HCP/pull/5896
   ```
   That will create a nicely formatted report in `./reports` according to [./reports/TEMPLATE.md][report-template]. Just FYI: [aro-hcp-test-alerts][aro-hcp-test-alerts-skill] does not need to be installed explicitly, because it's located in the `alert-tester` repo's `.claude/skills` dir.

> [!IMPORTANT]
>
> [aro-hcp-test-alerts][aro-hcp-test-alerts-skill] will use defaults (e.g. previous Mon–Sun week, across uksouth/eastus2/australiaeast), which you might have to adapt to your concrete scenario, e.g.
> ```text
> Run alert testing for PR https://github.com/Azure/ARO-HCP/pull/5896, past two whole weeks, uksouth only
> ```


### See also

* [alert-tester][alert-tester-repo] GitHub Repo
* [video][demo-video] and [notes][demo-notes] from alert-tester demo session

## Accessing PROD data with Grafana

### Explore Tab

To check your alert queries against prod data, use the Explore tab in the PROD Grafana instance (on a machine from which you can log in to your b- account). Once you've selected a datasource, you will be able to enter a PromQL query.

### Dashboard Development with Scratchpad

To develop and test dashboards against prod data, use the Scratchpad folder in the PROD Grafana instance:

* You can add and edit dashboards freely
* Dashboards auto-delete after 7 days, so save your JSONs often and always make a PR at the end

Look for the Scratchpad folder under Dashboards in the PROD Grafana instance.


## AI access

To get an overview of available Grafana URLs and data sources, make sure the [aro-hcp-env-info](https://github.com/openshift-online/aro-ai-tools/blob/main/skills/aro-hcp-env-info) from [aro-ai-tools](https://github.com/openshift-online/aro-ai-tools) is installed - and ask Copilot/Claude something like

```text
List all Grafana URLs for our different stages - including a list of available datasources
```

You can ask things directly, but results will depend on whether AI can figure out the correct metrics to check. If you know what you want to look at, best make it explicit

```text
What's the current cluster count across all prod regions using acm_managed_cluster_count
```

## Links

* [alert-tester repo][alert-tester-repo]
* [alert-tester README][alert-tester-readme]
* [aro-hcp-test-alerts skill][aro-hcp-test-alerts-skill]
* [aro-hcp-test-alerts report template][report-template]
* [Copilot setup guide][copilot-setup]
* [Demo video][demo-video]
* [Demo notes][demo-notes]

[alert-tester-repo]: https://github.com/mmazur/alert-tester
[alert-tester-readme]: https://github.com/mmazur/alert-tester/blob/main/README.md
[aro-hcp-test-alerts-skill]: https://github.com/mmazur/alert-tester/blob/main/.claude/skills/aro-hcp-test-alerts/SKILL.md
[report-template]: https://github.com/mmazur/alert-tester/blob/main/reports/TEMPLATE.md
[copilot-setup]: https://docs.google.com/document/d/1KUZSLknIkSd6usFPe_OcEYWJyW6mFeotc2lIsLgE3JA/edit?tab=t.ft6ndj5uukpn
[demo-video]: https://drive.google.com/file/d/1jkyx4_w8yzaybqhtukHuHizh2jFCTJf7/view
[demo-notes]: https://docs.google.com/document/d/1yvmf4MvOGpRf9VjA3Rnt30oNyfEFmE60oJeLxs0ek6w/edit?tab=t.0#heading=h.xr6j3y1ibl6b
