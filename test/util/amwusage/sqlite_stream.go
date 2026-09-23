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

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

type scanObservation struct {
	labels map[string]string
	// Preserve the exact value for diagnostics even when inventory validation
	// replaces invalid_value with its classification.
	raw     string
	at      float64
	value   *float64
	integer *int64
	invalid any
}
type scanResponseMeta struct {
	warnings, stats, resultType string
	cost                        *float64
	bytes                       int64
}
type scanDBError struct{ error }
type scanBodyReadError struct{ error }

func (e scanBodyReadError) Unwrap() error { return e.error }

type scanReadTracker struct {
	io.Reader
	err error
}

func (r *scanReadTracker) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil {
		r.err = err
	}
	return n, err
}

func validateScanCount(o *scanObservation) {
	if o.integer == nil {
		if o.invalid == nil {
			o.invalid = "not_nonnegative_integer"
		}
		o.value = nil
	}
}

// Decode result rows incrementally; only one series and a 128-observation batch
// are retained. HTTP callers publish only after this consumes and validates EOF.
func parseScanResponse(reader io.Reader, inventory bool, emit func([]scanObservation) error) (scanResponseMeta, error) {
	limit := int64(8 << 20)
	if inventory {
		limit = 32 << 20
	}
	tracked := &scanReadTracker{Reader: reader}
	r := &io.LimitedReader{R: tracked, N: limit + 1}
	d := json.NewDecoder(r)
	meta := scanResponseMeta{warnings: "[]", stats: "{}"}
	status, resultType := "", ""
	seenData, seenResult := false, false
	vectorRows, matrixRows := false, false
	batch := make([]scanObservation, 0, 128)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := emit(batch)
		batch = batch[:0]
		return err
	}
	delim := func(want json.Delim) error {
		v, err := d.Token()
		if err != nil {
			return err
		}
		if v != want {
			return fmt.Errorf("expected JSON %c", want)
		}
		return nil
	}
	parseObject := func(field func(string) error) error {
		if err := delim('{'); err != nil {
			return err
		}
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := t.(string)
			if !ok || seen[key] {
				return errors.New("invalid or duplicate JSON key")
			}
			seen[key] = true
			if err := field(key); err != nil {
				return err
			}
		}
		return delim('}')
	}
	// Unknown extensions are skipped token-by-token rather than materialized.
	skip := func() error {
		depth := 0
		for {
			token, err := d.Token()
			if err != nil {
				return err
			}
			if delimiter, ok := token.(json.Delim); ok {
				switch delimiter {
				case '{', '[':
					depth++
				case '}', ']':
					depth--
				}
			}
			if depth == 0 {
				return nil
			}
		}
	}
	parseErr := parseObject(func(key string) error {
		switch key {
		case "status":
			return d.Decode(&status)
		case "warnings":
			var warnings []string
			if err := d.Decode(&warnings); err != nil {
				return err
			}
			b, _ := json.Marshal(warnings)
			if len(b) > 65536 {
				return errors.New("warnings exceed 64 KiB")
			}
			meta.warnings = string(b)
			if len(warnings) > 0 {
				return errors.New("prometheus warnings: response not complete")
			}
			return nil
		case "error":
			var value any
			if err := d.Decode(&value); err != nil {
				return err
			}
			if value != nil && value != "" {
				return errors.New("prometheus API error")
			}
			return nil
		case "cost":
			return d.Decode(&meta.cost)
		case "data":
			seenData = true
			return parseObject(func(key string) error {
				switch key {
				case "resultType":
					return d.Decode(&resultType)
				case "stats":
					var stats json.RawMessage
					if err := d.Decode(&stats); err != nil {
						return err
					}
					if len(stats) > 65536 {
						return errors.New("query stats exceed 64 KiB")
					}
					meta.stats = string(stats)
					return nil
				case "result":
					seenResult = true
					if err := delim('['); err != nil {
						return err
					}
					for d.More() {
						var row struct {
							Metric map[string]string   `json:"metric"`
							Value  []json.RawMessage   `json:"value"`
							Values [][]json.RawMessage `json:"values"`
						}
						if err := parseObject(func(key string) error {
							switch key {
							case "metric":
								row.Metric = map[string]string{}
								return parseObject(func(name string) error {
									key := strings.ToLower(name)
									if _, exists := row.Metric[key]; exists {
										return errors.New("case-colliding label names")
									}
									var value string
									if err := d.Decode(&value); err != nil {
										return err
									}
									row.Metric[key] = strings.ToLower(value)
									return nil
								})
							case "value":
								return d.Decode(&row.Value)
							case "values":
								return d.Decode(&row.Values)
							default:
								return skip()
							}
						}); err != nil {
							return err
						}
						if row.Metric == nil {
							return errors.New("missing sample labels")
						}
						if row.Value != nil && row.Values != nil {
							return errors.New("ambiguous sample")
						}
						vectorRows = vectorRows || row.Value != nil
						matrixRows = matrixRows || row.Values != nil
						values := row.Values
						if row.Value != nil {
							values = [][]json.RawMessage{row.Value}
						}
						if values == nil {
							return errors.New("missing sample values")
						}
						for _, pair := range values {
							if len(pair) != 2 {
								return errors.New("invalid sample pair")
							}
							var at float64
							var text string
							if string(pair[0]) == "null" || json.Unmarshal(pair[0], &at) != nil || json.Unmarshal(pair[1], &text) != nil {
								return errors.New("invalid sample timestamp/value")
							}
							o := scanObservation{labels: row.Metric, at: at, raw: text}
							n, err := strconv.ParseFloat(text, 64)
							if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
								o.invalid = text
							} else {
								o.value = &n
								// Validate the exact decimal rather than a float rounded to
								// an integer (including values near int64's upper bound).
								if integer, err := strconv.ParseInt(text, 10, 64); err == nil && integer >= 0 {
									o.integer = &integer
								} else if n >= 0 && n < 9223372036854775808.0 && math.Trunc(n) == n && len(text) <= 128 {
									bounded := true
									if i := strings.IndexAny(text, "eE"); i >= 0 {
										exponent, err := strconv.ParseInt(text[i+1:], 10, 32)
										bounded = err == nil && exponent >= -400 && exponent <= 400
									}
									if bounded {
										if exact, ok := new(big.Rat).SetString(text); ok && exact.IsInt() && exact.Num().IsInt64() {
											integer := exact.Num().Int64()
											if integer >= 0 {
												o.integer = &integer
											}
										}
									}
								}
							}
							if inventory && (o.integer == nil || *o.integer < 1) {
								o.invalid = "not_positive_integer"
								o.value = nil
								o.integer = nil
							}
							batch = append(batch, o)
							if len(batch) == cap(batch) {
								if err := flush(); err != nil {
									return err
								}
							}
						}
					}
					return delim(']')
				default:
					return skip()
				}
			})
		default:
			return skip()
		}
	})
	if parseErr == nil {
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			if err != nil {
				parseErr = err
			} else {
				parseErr = errors.New("trailing response")
			}
		}
	}
	meta.bytes = limit + 1 - r.N
	meta.resultType = resultType
	if meta.bytes > limit {
		parseErr = errors.New("response exceeds size limit")
	} else if parseErr != nil && ((tracked.err != nil && tracked.err != io.EOF) || errors.Is(parseErr, io.ErrUnexpectedEOF) || errors.Is(parseErr, io.EOF)) {
		parseErr = scanBodyReadError{parseErr}
	}
	if parseErr == nil && (status != "success" || !seenData || !seenResult || (resultType != "vector" && resultType != "matrix") || (inventory && resultType != "vector")) {
		parseErr = errors.New("invalid Prometheus success envelope")
	}
	if parseErr == nil && ((resultType == "vector" && matrixRows) || (resultType == "matrix" && vectorRows)) {
		parseErr = errors.New("sample shape differs from result type")
	}
	if parseErr == nil {
		parseErr = flush()
	}
	return meta, parseErr
}

func insertObservations(ctx context.Context, tx *sql.Tx, aid int64, batch []scanObservation) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO observation(attempt_id,labelset_id,timestamp,value,integer_value,invalid_value) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return scanDBError{err}
	}
	defer stmt.Close()
	for _, o := range batch {
		lid, err := internLabels(ctx, tx, o.labels)
		if err != nil {
			return scanDBError{err}
		}
		if _, err := stmt.ExecContext(ctx, aid, lid, o.at, o.value, o.integer, o.invalid); err != nil {
			var sqliteError *sqlite.Error
			if errors.As(err, &sqliteError) && (sqliteError.Code() == 1555 || sqliteError.Code() == 2067) {
				return errors.New("duplicate normalized observation")
			}
			return scanDBError{err}
		}
	}
	return nil
}

func compressScanError(body string) ([]byte, bool) {
	truncated := len(body) > 65536
	if truncated {
		body = body[:65536]
	}
	var buffer bytes.Buffer
	w := gzip.NewWriter(&buffer)
	_, _ = io.WriteString(w, body)
	_ = w.Close()
	return buffer.Bytes(), truncated
}
