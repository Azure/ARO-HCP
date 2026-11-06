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

package cipatterns

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata/query"

	"github.com/Azure/ARO-HCP/tooling/cifailurepatterns"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cifailures"
)

func decodeObservation(row query.Row) (cifailures.Observation, error) {
	var o cifailures.Observation
	for _, column := range []struct {
		name  string
		value *string
	}{
		{"origin", &o.Origin}, {"buildId", &o.BuildID}, {"source", &o.Source}, {"testId", &o.TestID}, {"message", &o.Text}, {"testName", &o.TestName},
	} {
		value, err := row.StringByName(column.name)
		if err != nil {
			return o, fmt.Errorf("read %s: %w", column.name, err)
		}
		*column.value = value
	}
	for _, column := range []struct {
		name  string
		value *time.Time
	}{
		{"startedAt", &o.StartedAt}, {"finishedAt", &o.FinishedAt}, {"setupFinishTime", &o.SetupFinishTime}, {"testStartTime", &o.TestStartTime}, {"cleanupStartTime", &o.CleanupStartTime},
	} {
		value, err := row.DateTimeByName(column.name)
		if err != nil {
			return o, fmt.Errorf("read %s: %w", column.name, err)
		}
		if value != nil {
			*column.value = value.UTC()
		}
	}
	return o, nil
}

func decodeInputs(rows []query.Row) ([]input, error) {
	inputs := make([]input, 0, len(rows))
	for index, row := range rows {
		o, err := decodeObservation(row)
		if err != nil {
			return nil, fmt.Errorf("input row %d: %w", index, err)
		}
		multiplicity, err := row.LongByName("multiplicity")
		if err != nil || multiplicity == nil {
			return nil, fmt.Errorf("input row %d invalid multiplicity: %v", index, err)
		}
		variants, err := row.LongByName("nameVariantCount")
		if err != nil || variants == nil {
			return nil, fmt.Errorf("input row %d invalid name variants: %v", index, err)
		}
		status, err := row.StringByName("nameStatus")
		if err != nil {
			return nil, fmt.Errorf("input row %d name status: %w", index, err)
		}
		o.NameVariants = *variants
		inputs = append(inputs, input{observation: o, multiplicity: *multiplicity, nameStatus: status})
	}
	return inputs, nil
}

func decodeResults(rows []query.Row) ([]resultRow, error) {
	results := make([]resultRow, 0, len(rows))
	for index, row := range rows {
		o, err := decodeObservation(row)
		if err != nil {
			return nil, fmt.Errorf("result row %d: %w", index, err)
		}
		r := rawEnvelope(o)
		for _, column := range []struct {
			name  string
			value *string
		}{
			{"resultKey", &r.ResultKey}, {"rawFingerprint", &r.RawFingerprint}, {"extractorVersion", &r.ExtractorVersion}, {"producerCommit", &r.ProducerCommit},
		} {
			value, err := row.StringByName(column.name)
			if err != nil {
				return nil, err
			}
			*column.value = value
		}
		schema, err := row.LongByName("resultSchemaVersion")
		if err != nil || schema == nil {
			return nil, fmt.Errorf("result row %d invalid schema: %v", index, err)
		}
		r.ResultSchemaVersion = *schema
		computed, err := row.DateTimeByName("computedAt")
		if err != nil || computed == nil {
			return nil, fmt.Errorf("result row %d invalid computedAt: %v", index, err)
		}
		r.ComputedAt = computed.UTC()
		findings, err := row.DynamicByName("findings")
		if err != nil {
			return nil, err
		}
		r.Findings, err = decodeFindings(findings)
		if err != nil {
			return nil, fmt.Errorf("result row %d findings: %w", index, err)
		}
		results = append(results, r)
	}
	return results, nil
}

// Require the complete typed JSON shape, including explicit zero byte offsets.
// Unknown fields must not disappear before duplicate comparisons.
func decodeFindings(encoded []byte) ([]cifailurepatterns.Finding, error) {
	var findings []cifailurepatterns.Finding
	if err := json.Unmarshal(encoded, &findings); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(findings)
	if err != nil {
		return nil, err
	}
	var actual, expected any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(canonical, &expected); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(actual, expected) {
		return nil, fmt.Errorf("findings do not match complete schema-%d shape", resultSchemaVersion)
	}
	return findings, nil
}
