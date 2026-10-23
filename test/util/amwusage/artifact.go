// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package amwusage

import "encoding/json"

// These are read-only views of the archived schemaVersion 1 format. The original
// JSON is retained separately so unknown provenance fields survive rendering.
type artifactData struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Run           *artifactRun              `json:"run"`
	Workspaces    []artifactWorkspace       `json:"workspaces"`
	Manifests     []artifactRecord          `json:"manifests"`
	Records       map[string]artifactRecord `json:"records"`
	Complete      *bool                     `json:"complete"`
	Errors        []string                  `json:"errors"`
	Context       *artifactContext          `json:"context,omitempty"`
}

type artifactContext struct {
	SchemaVersion int `json:"schemaVersion"`
	Run           struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Prow  string `json:"prow"`
	} `json:"run"`
	Baseline struct {
		Start   string   `json:"start"`
		End     string   `json:"end"`
		Reason  string   `json:"reason"`
		Sources []string `json:"sources"`
	} `json:"baseline"`
	Clusters    []artifactCluster `json:"clusters"`
	Limitations []string          `json:"limitations"`
}

type artifactCluster struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ResourceID string   `json:"resourceId"`
	Namespaces []string `json:"namespaces"`
	SourceURLs []string `json:"sourceURLs"`
}

type artifactRun struct {
	Job           string         `json:"job"`
	Build         string         `json:"build"`
	Prow          string         `json:"prow"`
	Start         float64        `json:"start"`
	End           float64        `json:"end"`
	PlatformStart int64          `json:"platformStart"`
	PlatformEnd   int64          `json:"platformEnd"`
	Started       map[string]any `json:"started"`
	Finished      map[string]any `json:"finished"`
}

type artifactWorkspace struct {
	Name        string            `json:"name"`
	ID          string            `json:"id"`
	Endpoint    string            `json:"endpoint"`
	Names       []string          `json:"names"`
	Discovery   string            `json:"discovery"`
	Platform    map[string]string `json:"platform"`
	Metrics     []artifactMetric  `json:"metrics"`
	Inventories []seriesInventory `json:"inventories,omitempty"`
}

type artifactMetric struct {
	Name                 string `json:"name"`
	Instant              string `json:"instant"`
	Range                string `json:"range"`
	InstantQuery         string `json:"instantQuery"`
	RangeQuery           string `json:"rangeQuery"`
	NewSeries            string `json:"newSeries,omitempty"`
	NewSeriesQuery       string `json:"newSeriesQuery,omitempty"`
	Samples              string `json:"samples,omitempty"`
	SamplesQuery         string `json:"samplesQuery,omitempty"`
	BaselineSeries       string `json:"baselineSeries,omitempty"`
	BaselineSeriesQuery  string `json:"baselineSeriesQuery,omitempty"`
	BaselineSamples      string `json:"baselineSamples,omitempty"`
	BaselineSamplesQuery string `json:"baselineSamplesQuery,omitempty"`
}

type artifactRecord struct {
	Request struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		URL  string `json:"url"`
	} `json:"request"`
	OK            bool            `json:"ok"`
	Body          string          `json:"body"`
	Status        *int            `json:"status"`
	Error         json.RawMessage `json:"error"`
	ElapsedMS     float64         `json:"elapsedMs"`
	ResponseBytes *float64        `json:"responseBytes"`
	APICost       *float64        `json:"apiCost"`
	StartedAt     string          `json:"startedAt"`
	FinishedAt    string          `json:"finishedAt"`
}

type promResponse struct {
	Status   string   `json:"status"`
	Warnings []string `json:"warnings"`
	Data     struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string   `json:"metric"`
			Value  []json.RawMessage   `json:"value"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type platformResponse struct {
	Interval string `json:"interval"`
	Value    []struct {
		ErrorCode  string `json:"errorCode"`
		Timeseries []struct {
			MetadataValues []struct {
				Name struct {
					Value string `json:"value"`
				} `json:"name"`
				Value string `json:"value"`
			} `json:"metadatavalues"`
			Data []struct {
				Timestamp string          `json:"timeStamp"`
				Maximum   json.RawMessage `json:"maximum"`
			} `json:"data"`
		} `json:"timeseries"`
	} `json:"value"`
}

type renderPoint struct {
	Time  float64
	Value *float64
}

type renderGroup struct {
	Labels map[string]string
	Count  *float64
}

type metricAnalysis struct {
	artifactMetric
	Groups                                                                       []renderGroup
	Values                                                                       []renderPoint
	InstantOK, RangeOK                                                           bool
	InstantState, RangeState                                                     string
	Series, PeakSamplesPerMinute, ObservedSamples                                *float64
	Expected, Valid, InvalidPoints, InvalidEvaluations, InvalidCardinalityGroups int
	OutOfRangePoints, DuplicatePoints                                            int
	TrailingSeconds                                                              float64
	Warnings                                                                     []string
	Chart                                                                        renderChart
}

type platformAnalysis struct {
	ID, State string
	Series    []platformSeriesAnalysis
}

type platformSeriesAnalysis struct {
	Labels                                                                map[string]string
	Values                                                                []renderPoint
	Expected, Rows, Valid, Missing, DuplicateTimestamps, OutOfRangePoints int
	Interval                                                              string
	First, Last, Max                                                      *float64
}

type quotaAnalysis struct {
	Usage, Limit      string
	Complete          bool
	Paired, Crossings int
	MaxRatio          *float64
	Chart             renderChart
}

type workspaceAnalysis struct {
	artifactWorkspace
	Platform          map[string]platformAnalysis
	Quota             []quotaAnalysis
	MetricResults     []metricAnalysis
	Succeeded         int
	SelectedPercent   string
	NoLimitThrottling bool
	Verdict           string
	DiscoveryState    string
}

type renderAnalysis struct {
	Workspaces                                               []workspaceAnalysis
	Requests, Errors, PromQL, PromQLErrors, PlatformRequests int
	PlatformErrors, ByteReports, CostReports                 int
	ElapsedMS, ResponseBytes, APICost                        float64
	FirstRequest, LastRequest                                string
}

type renderChart struct {
	Title      string
	Lines      []chartLine
	Ticks      []chartTick
	Times      []chartTick
	RunMarkers []string
	HasData    bool
}

type chartLine struct {
	Name, Color, Path string
	Dashed            bool
	Dots              []chartDot
}

type chartDot struct {
	X, Y, Title string
}

type chartTick struct {
	Position, Label, Anchor string
}
