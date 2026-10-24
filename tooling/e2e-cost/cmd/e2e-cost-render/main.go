// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	cost "github.com/Azure/ARO-HCP/tooling/e2e-cost"
)

func main() {
	os.Exit(run())
}

func run() int {
	input := flag.String("input", "run-data.json", "Render-ready JSON snapshot path")
	output := flag.String("output", "cost-summary.html", "Report path")
	format := flag.String("format", "html", "html for a self-contained report; json for the site viewer, gzip-compressed when the output ends in .gz")
	flag.Parse()
	if flag.NArg() != 0 || *input == *output || (*format != "html" && *format != "json") {
		flag.Usage()
		return 2
	}
	snapshot, err := cost.ReadSnapshot(*input)
	if err == nil {
		err = cost.WriteFile(*output, func(w io.Writer) error {
			if *format == "html" {
				return cost.Render(w, snapshot)
			}
			if !strings.HasSuffix(*output, ".gz") {
				return cost.RenderJSON(w, snapshot)
			}
			gz := gzip.NewWriter(w)
			if err := cost.RenderJSON(gz, snapshot); err != nil {
				return err
			}
			return gz.Close()
		})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Render: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", *output)
	return 0
}
