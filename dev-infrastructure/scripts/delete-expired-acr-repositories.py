#!/usr/bin/env python3
"""Delete whole ACR repositories that have not been updated for a number of days.

Runs nightly as an ACR task defined in templates/dev-acr.bicep, using the
registry login every task run receives. The only argument is base64-encoded
JSON settings:

  registry           registry name
  repositoryPattern  regular expression a repository name must match
  deleteAfterDays    delete repositories not updated for this many days
  dryRun             only report what would be deleted (default false)
  maxDeletions       stop deleting after this many repositories (default no limit)

Set ACR_REFRESH_TOKEN to run it outside a task, for example with the output of
`az acr login --name <registry> --expose-token --query accessToken -o tsv`.
"""
import base64
import concurrent.futures
import datetime
import http.client
import json
import os
import re
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

# Age is measured from the last push: CI reruns can reuse a namespace, and so
# an existing repository. Empty repositories have nothing left to pull, so they
# only wait long enough to never interrupt a push in progress.
EMPTY_DELETE_AFTER = datetime.timedelta(days=1)
PARALLELISM = 8
RETRIES = 8


def with_retries(call):
    """Retry network errors, throttling and server errors with backoff."""
    for attempt in range(RETRIES):
        delay = min(2 ** attempt, 30)
        try:
            return call()
        except urllib.error.HTTPError as e:
            if e.code != 429 and e.code < 500 or attempt == RETRIES - 1:
                raise
            retry_after = e.headers.get("Retry-After", "") if e.headers else ""
            if retry_after.isdigit():
                delay = int(retry_after)
        except (OSError, http.client.HTTPException):
            if attempt == RETRIES - 1:
                raise
        time.sleep(delay)


class Registry:
    def __init__(self, name, refresh_token):
        self.host = f"{name}.azurecr.io"
        self.refresh_token = refresh_token

    def _token(self, scope):
        body = urllib.parse.urlencode({
            "grant_type": "refresh_token",
            "service": self.host,
            "scope": scope,
            "refresh_token": self.refresh_token,
        }).encode()
        def call():
            with urllib.request.urlopen(f"https://{self.host}/oauth2/token", body, timeout=60) as response:
                return json.load(response)["access_token"]
        return with_retries(call)

    def _request(self, method, path, scope):
        request = urllib.request.Request(
            f"https://{self.host}{path}",
            method=method,
            headers={"Authorization": f"Bearer {self._token(scope)}"},
        )

        def call():
            with urllib.request.urlopen(request, timeout=60) as response:
                data = response.read()
                return (json.loads(data) if data else None), response.headers.get("Link")
        return with_retries(call)

    def repositories(self):
        # The registry caps page sizes and links to the next page.
        path, seen = "/acr/v1/_catalog?n=1000", set()
        while path:
            if path in seen:
                raise RuntimeError(f"catalog paging repeated {path}")
            seen.add(path)
            page, link = self._request("GET", path, "registry:catalog:*")
            yield from page.get("repositories") or []
            if not link:
                return
            match = re.fullmatch(r'\s*<([^>]+)>;\s*rel="next"\s*', link)
            if not match:
                raise RuntimeError(f"unexpected catalog Link header {link!r}")
            path = match.group(1).removeprefix(f"https://{self.host}")
            if not path.startswith("/acr/v1/_catalog?"):
                raise RuntimeError(f"unexpected catalog continuation {path!r}")

    def metadata(self, repository):
        return self._request("GET", f"/acr/v1/{repository}", f"repository:{repository}:metadata_read")[0]

    def delete(self, repository):
        self._request("DELETE", f"/acr/v1/{repository}", f"repository:{repository}:delete")


def parse_time(value):
    return datetime.datetime.fromisoformat(re.sub(r"(\.\d{6})\d*", r"\1", value.replace("Z", "+00:00")))


def expired(metadata, now, delete_after):
    updated = parse_time(metadata["lastUpdateTime"])
    count = metadata["manifestCount"]
    if not isinstance(count, int):
        raise ValueError(f"unexpected manifestCount {count!r}")
    return now - updated > delete_after or (count == 0 and now - updated > EMPTY_DELETE_AFTER)


def main():
    settings = json.loads(base64.b64decode(sys.argv[1]))
    registry_name = settings["registry"]
    pattern = re.compile(settings["repositoryPattern"])
    delete_after = datetime.timedelta(days=int(settings["deleteAfterDays"]))
    dry_run = bool(settings.get("dryRun", False))
    max_deletions = settings.get("maxDeletions")

    refresh_token = os.environ.get("ACR_REFRESH_TOKEN")
    if not refresh_token:
        with open(os.path.expanduser("~/.docker/config.json")) as f:
            refresh_token = json.load(f)["auths"][f"{registry_name}.azurecr.io"]["identitytoken"]
    registry = Registry(registry_name, refresh_token)
    now = datetime.datetime.now(datetime.timezone.utc)

    claimed = 0
    lock = threading.Lock()

    def claim():
        nonlocal claimed
        with lock:
            if max_deletions is not None and claimed >= max_deletions:
                return False
            claimed += 1
            return True

    def process(repository):
        try:
            metadata = registry.metadata(repository)
        except urllib.error.HTTPError as e:
            return ("gone", "") if e.code == 404 else ("failed", f"metadata HTTP {e.code}")
        except (OSError, http.client.HTTPException) as e:
            return ("failed", f"metadata {getattr(e, 'reason', e)}")
        try:
            if not expired(metadata, now, delete_after):
                return ("kept", "")
        except (KeyError, TypeError, ValueError) as e:
            return ("failed", f"unexpected metadata: {e}")
        if not claim():
            return ("skipped", "maxDeletions reached")
        if dry_run:
            return ("would-delete", "")
        try:
            registry.delete(repository)
        except urllib.error.HTTPError as e:
            # Listed in the catalog but unknown to the registry.
            return ("orphaned", "") if e.code == 404 else ("failed", f"delete HTTP {e.code}")
        except (OSError, http.client.HTTPException) as e:
            return ("failed", f"delete {getattr(e, 'reason', e)}")
        return ("deleted", "")

    candidates = [r for r in registry.repositories() if pattern.search(r)]
    counts = {}
    with concurrent.futures.ThreadPoolExecutor(PARALLELISM) as pool:
        for repository, (outcome, detail) in zip(candidates, pool.map(process, candidates)):
            counts[outcome] = counts.get(outcome, 0) + 1
            if outcome != "kept":
                print(f"{outcome} {repository}" + (f": {detail}" if detail else ""), flush=True)
    print(f"{len(candidates)} matching repositories: {json.dumps(counts, sort_keys=True)}")
    sys.exit(1 if counts.get("failed") else 0)


if __name__ == "__main__":
    main()
