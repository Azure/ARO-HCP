# Alert Verification

When you write an alert, you're usually not so sure about how it will behave with real production monitoring data.

Standard approach:
1. Come up with an alert
2. Push to prod (days to weeks)
3. Wait weeks to see how it performs
4. Guess how to correct it
5. Push to prod (days to weeks)
6. Hopefully you don't need to repeat

Luckily with [alert-tester][alert-tester-repo] we have a faster approach (in case the monitoring data is already present in PROD):

1. Come up with an alert
2. Analyze how it performs against historical prod data and tune it (day)
3. Push to prod (days to weeks)
4. Done

> [!IMPORTANT]
>
> All commands below need to be executed on a machine from which you can log in to your b- account!

## Verifying alerts with [alert-tester][alert-tester-repo]

In a bash shell (on a machine from which you can log in to your b- account), e.g. WSL, Linux, or Git Bash:
```bash
git clone https://github.com/mmazur/alert-tester
cd alert-tester
make build
```

You can now use `alert-tester`, e.g.

```bash
export ATEST_GRAFANA_BEARER_TOKEN=$(az account get-access-token \
      --resource ce34e7e5-485f-4d76-964f-b3d2b16d1e4f \
      --query accessToken -o tsv)

./atest grafana \
      --grafana-url https://arohcp-prod-g5d9a9akashnb5gd.suk.grafana.azure.com/ \
      --datasource services-australiaeast \
      -q 'time() - max without(prometheus_replica) (kube_lease_renew_time{namespace=~"^(kube-applier)$"})' \
      --gt 45,60,90,120,180 \
      --for 30s,1m,2m,3m \
      --from 2026-06-23 \
      --to 2026-07-09
```

This will give you a nice output like

```
...

expr: time() - max without (prometheus_replica) (kube_lease_renew_time{namespace=~"^(kube-applier)$"})

local threshold > 180: 5549 samples pass
analysis:
- for 30s: 4 firings
- for 1m: 4 firings
- for 2m: 3 firings
- for 3m: 2 firings
```

For more options and a detailed description, see [README.md][alert-tester-readme].

### AI-Generated Reports

The basic `atest` tool usage is already very helpful. But if you want to check multiple data sources and have a nice report, you might want to use the [aro-hcp-test-alerts skill][aro-hcp-test-alerts-skill]. To do so, you can:

1. Make sure you have [alert-tester][alert-tester-repo] cloned and built on a machine from which you can log in to your b- account (see above)
2. [Make sure you have Copilot properly set up][copilot-setup]
3. Have Copilot (or Claude with Copilot access) run from within the `alert-tester` dir
4. Ask Copilot something like
   ```
   /aro-hcp-test-alerts review PR https://github.com/Azure/ARO-HCP/pull/5896
   ```
   That will create a nicely formatted report in `./reports` according to [./reports/TEMPLATE.md][report-template]. Just FYI: [aro-hcp-test-alerts][aro-hcp-test-alerts-skill] does not need to be installed explicitly, because it's located in the `alert-tester` repo's `.claude/skills` dir.

> [!IMPORTANT]
>
> [aro-hcp-test-alerts][aro-hcp-test-alerts-skill] will use defaults (e.g. previous Mon–Sun week, across uksouth/eastus2/australiaeast), which you might have to adapt to your concrete scenario, e.g.
> ```
> /aro-hcp-test-alerts review PR https://github.com/Azure/ARO-HCP/pull/5896 querying the last two weeks
> ```


### See also

* [alert-tester][alert-tester-repo] GitHub Repo
* [video][demo-video] and [notes][demo-notes] from alert-tester and grafana-datasource demo session

## Accessing PROD data with Grafana

### Ad Hoc Explorer

If you want to see your query results in PROD Grafana, you can use the Ad Hoc Explorer (on a machine from which you can log in to your b- account):

* [Ad Hoc Explorer][grafana-adhoc-explorer]

Once you've selected a datasource, you will be able to enter a PromQL query.

### Grafana Datasource

[grafana-datasource][grafana-datasource-repo] is a tool that automates Grafana datasource configuration. You can set it up on a machine from which you can log in to your b- account:

1. [Make sure you have Copilot properly set up][copilot-setup]
2. Clone [grafana-datasource][grafana-datasource-repo]
3. Run Copilot from within the `grafana-datasource` directory and ask it to set up Grafana

## Links

* [alert-tester repo][alert-tester-repo]
* [alert-tester README][alert-tester-readme]
* [aro-hcp-test-alerts skill][aro-hcp-test-alerts-skill]
* [aro-hcp-test-alerts report template][report-template]
* [Copilot setup guide][copilot-setup]
* [Demo video][demo-video]
* [Demo notes][demo-notes]
* [Grafana Ad Hoc Explorer][grafana-adhoc-explorer]
* [grafana-datasource repo][grafana-datasource-repo]

[alert-tester-repo]: https://github.com/mmazur/alert-tester
[alert-tester-readme]: https://github.com/mmazur/alert-tester/blob/main/README.md
[aro-hcp-test-alerts-skill]: https://github.com/mmazur/alert-tester/blob/main/.claude/skills/aro-hcp-test-alerts/SKILL.md
[report-template]: https://github.com/mmazur/alert-tester/blob/main/reports/TEMPLATE.md
[copilot-setup]: https://docs.google.com/document/d/1KUZSLknIkSd6usFPe_OcEYWJyW6mFeotc2lIsLgE3JA/edit?tab=t.ft6ndj5uukpn
[demo-video]: https://drive.google.com/file/d/1jkyx4_w8yzaybqhtukHuHizh2jFCTJf7/view
[demo-notes]: https://docs.google.com/document/d/1yvmf4MvOGpRf9VjA3Rnt30oNyfEFmE60oJeLxs0ek6w/edit?tab=t.0#heading=h.xr6j3y1ibl6b
[grafana-adhoc-explorer]: https://arohcp-prod-g5d9a9akashnb5gd.suk.grafana.azure.com/d/adhoc-explorer/ad-hoc-explorer
[grafana-datasource-repo]: https://github.com/mmazur/grafana-datasource
