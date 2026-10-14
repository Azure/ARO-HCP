# Offline ECharts Runtime

Apache ECharts **5.6.0**, the upstream packaged, minified full distribution.
No build or modification was applied. The report embeds this file verbatim;
there are no runtime CDN requests. go-echarts v2.6.7 generates chart options,
matching the existing Go workspace dependency.

Downloaded from the version-pinned npm package on jsDelivr:

| File | Source | SHA-256 |
| --- | --- | --- |
| `echarts-5.6.0.min.js` | https://cdn.jsdelivr.net/npm/echarts@5.6.0/dist/echarts.min.js | `bf4a223524e40b77c304bec67e1222cf551f14880cf42c69dc046558e11c07b1` |
| `LICENSE` | https://cdn.jsdelivr.net/npm/echarts@5.6.0/LICENSE | `634293835b43a6dd2094fa39182a3d9a6b9ca43b7fdb9ac354e8037af2a3093a` |
| `NOTICE` | https://cdn.jsdelivr.net/npm/echarts@5.6.0/NOTICE | `fa99ac3af859d0e13166906dc53a73ad34a08898da7e8ae83407275496e9e30c` |

Upstream: https://github.com/apache/echarts/tree/5.6.0
The accompanying upstream LICENSE includes third-party notices. LICENSE and
NOTICE are also embedded into each generated report for offline distribution.

`LICENSE-d3` is reproduced from
https://cdn.jsdelivr.net/npm/echarts@5.6.0/licenses/LICENSE-d3 and embedded in the
report as well. It covers the d3-derived algorithms referenced by ECharts' LICENSE.
