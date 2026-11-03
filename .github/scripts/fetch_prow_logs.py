#!/usr/bin/env python3
# Copyright 2026 Microsoft Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Fetch finished.json and build-log.txt from the exact bucket in a Prow URL.

Usage: fetch_prow_logs.py URL OUTPUT_DIR
Existing artifact and manifest outputs are refused, never reused or overwritten.
Success saves manifest.json and prints the same JSON; failures publish no new files.
"""

import argparse
import http.client
import json
import os
import re
import sys
import tempfile
import urllib.request
from pathlib import Path


MAX_BYTES = 64 * 1024 * 1024
CHUNK_SIZE = 64 * 1024
TIMEOUT = 60
ARTIFACTS = ("finished.json", "build-log.txt")
BUCKET = r"[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]"
SEGMENT = r"[A-Za-z0-9][A-Za-z0-9_.-]*"
PROW_URL = re.compile(
    rf"https://prow\.ci\.openshift\.org/view/gs/(?P<bucket>{BUCKET})/"
    rf"(?P<path>(?:pr-logs/pull/{SEGMENT}/[0-9]+|logs)/{SEGMENT}/[0-9]+)/?"
)


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def artifact_base(url):
    """Validate a canonical Prow run URL and preserve its bucket and job path."""
    match = PROW_URL.fullmatch(url) if isinstance(url, str) else None
    if match is None:
        raise ValueError(f"unsupported or malformed Prow run URL: {url!r}")
    return (
        f"https://storage.googleapis.com/{match['bucket']}/{match['path']}/"
    )


def _download(opener, url, destination):
    try:
        with opener.open(url, timeout=TIMEOUT) as response:
            if response.status != 200:
                raise ValueError(f"HTTP status {response.status}")
            expected_size = response.headers.get("Content-Length")
            if expected_size is not None:
                if not re.fullmatch(r"[0-9]+", expected_size):
                    raise ValueError("invalid Content-Length")
                expected_size = int(expected_size)
            size = 0
            with destination.open("wb") as output:
                while True:
                    chunk = response.read(CHUNK_SIZE)
                    if not chunk:
                        break
                    size += len(chunk)
                    if size > MAX_BYTES:
                        raise ValueError(f"artifact exceeds {MAX_BYTES} bytes")
                    output.write(chunk)
            if expected_size is not None and size != expected_size:
                raise ValueError(f"incomplete artifact: expected {expected_size}, got {size} bytes")
        if not size:
            raise ValueError("empty artifact")
    except (OSError, ValueError, http.client.HTTPException) as error:
        raise ValueError(f"fetch failed for {url}: {error}") from error


def fetch_logs(url, output_dir):
    """Save both artifacts and manifest.json, returning the manifest object.

    No redirects or bucket fallbacks are followed. Each download has a 60-second
    socket timeout and a 64-MiB size limit. Temporary files are removed on failure;
    exclusive publication prevents overwriting existing artifacts or manifests.
    """
    base = artifact_base(url)
    directory = Path(output_dir).resolve()
    directory.mkdir(parents=True, exist_ok=True)
    destinations = {name: directory / name for name in ARTIFACTS}
    outputs = {**destinations, "manifest.json": directory / "manifest.json"}
    for destination in outputs.values():
        if os.path.lexists(destination):
            raise ValueError(f"refusing existing artifact output: {destination}")
    manifest = {
        "prow_url": url,
        "artifact_base": base,
        "files": {name: str(destination) for name, destination in destinations.items()},
    }

    opener = urllib.request.build_opener(_NoRedirect())
    published = []
    with tempfile.TemporaryDirectory(prefix=".fetch-prow-", dir=directory) as staging:
        staged = Path(staging)
        for name in ARTIFACTS:
            _download(opener, base + name, staged / name)
            if name == "finished.json":
                try:
                    with (staged / name).open("rb") as finished:
                        metadata = json.load(finished)
                    if not isinstance(metadata, dict) or not isinstance(
                        metadata.get("result"), str
                    ) or not metadata["result"].strip():
                        raise ValueError("expected an object with a nonblank string result")
                except (ValueError, OSError) as error:
                    raise ValueError(f"invalid {base + name}: {error}") from error
        (staged / "manifest.json").write_text(json.dumps(manifest) + "\n", encoding="utf-8")
        try:
            for name, destination in outputs.items():
                os.link(staged / name, destination)
                published.append(destination)
        except OSError:
            for destination in published:
                destination.unlink()
            raise
    return manifest


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url", help="canonical prow.ci.openshift.org run URL")
    parser.add_argument("output_dir", help="directory without existing artifact outputs")
    args = parser.parse_args(argv)
    try:
        manifest = fetch_logs(args.url, args.output_dir)
    except (OSError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    print(json.dumps(manifest))
    return 0


if __name__ == "__main__":
    sys.exit(main())
