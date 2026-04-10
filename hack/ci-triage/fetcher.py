"""HTTP fetching layer for CI triage tooling.

Provides Fetcher for direct HTTP access and CachedFetcher
for disk-cached retrieval of immutable GCS artifacts.
"""

import hashlib
import json
import os
import urllib.error
import urllib.request


class Fetcher:
    """HTTP fetcher for GCS and JSON endpoints."""

    _ERROR_KINDS = ("not_found", "timeout", "http",
                    "network", "parse")

    def __init__(self):
        self._errors = {k: 0 for k in self._ERROR_KINDS}

    def _record_error(self, kind):
        self._errors[kind] = self._errors.get(kind, 0) + 1

    @property
    def errors(self):
        return dict(self._errors)

    def fetch(self, url, timeout=15):
        try:
            with urllib.request.urlopen(url, timeout=timeout) as r:
                return r.read()
        except urllib.error.HTTPError as e:
            self._record_error(
                "not_found" if e.code == 404 else "http")
            return None
        except TimeoutError:
            self._record_error("timeout")
            return None
        except (urllib.error.URLError, OSError):
            self._record_error("network")
            return None

    def fetch_text(self, url, timeout=15):
        try:
            with urllib.request.urlopen(url, timeout=timeout) as r:
                ct = r.headers.get("Content-Type", "")
                if "text/html" in ct:
                    return None
                return r.read().decode("utf-8", errors="replace")
        except urllib.error.HTTPError as e:
            self._record_error(
                "not_found" if e.code == 404 else "http")
            return None
        except TimeoutError:
            self._record_error("timeout")
            return None
        except (urllib.error.URLError, OSError):
            self._record_error("network")
            return None

    def fetch_json(self, url, timeout=15):
        try:
            with urllib.request.urlopen(url, timeout=timeout) as r:
                if "text/html" in r.headers.get("Content-Type", ""):
                    return None
                return json.loads(r.read())
        except urllib.error.HTTPError as e:
            self._record_error(
                "not_found" if e.code == 404 else "http")
            return None
        except TimeoutError:
            self._record_error("timeout")
            return None
        except json.JSONDecodeError:
            self._record_error("parse")
            return None
        except (urllib.error.URLError, OSError):
            self._record_error("network")
            return None


class CachedFetcher(Fetcher):
    """Fetcher with disk cache for immutable GCS artifacts.

    Caches junit.xml and build-log.txt responses keyed by
    URL hash. Non-artifact URLs are never cached.
    """

    def __init__(self, cache_dir=None):
        super().__init__()
        if cache_dir is None:
            xdg = os.environ.get(
                "XDG_CACHE_HOME",
                os.path.expanduser("~/.cache"))
            cache_dir = os.path.join(xdg, "ci-triage")
        self._cache_dir = cache_dir
        self._hits = 0
        self._misses = 0

    @staticmethod
    def _is_cacheable(url):
        return ("test-platform-results" in url
                and "/storage/v1/b/" not in url)

    def _cache_path(self, url, suffix):
        key = hashlib.sha256(url.encode()).hexdigest()[:16]
        return os.path.join(self._cache_dir, f"{key}{suffix}")

    def fetch(self, url, timeout=15):
        if not self._is_cacheable(url):
            return super().fetch(url, timeout)
        path = self._cache_path(url, ".bin")
        if os.path.exists(path):
            self._hits += 1
            with open(path, "rb") as f:
                return f.read()
        self._misses += 1
        data = super().fetch(url, timeout)
        if data is not None:
            os.makedirs(self._cache_dir, exist_ok=True)
            with open(path, "wb") as f:
                f.write(data)
        return data

    def fetch_text(self, url, timeout=15):
        if not self._is_cacheable(url):
            return super().fetch_text(url, timeout)
        path = self._cache_path(url, ".txt")
        if os.path.exists(path):
            self._hits += 1
            with open(path, "r") as f:
                return f.read()
        self._misses += 1
        text = super().fetch_text(url, timeout)
        if text is not None:
            os.makedirs(self._cache_dir, exist_ok=True)
            with open(path, "w") as f:
                f.write(text)
        return text

    def fetch_json(self, url, timeout=15):
        if not self._is_cacheable(url):
            return super().fetch_json(url, timeout)
        path = self._cache_path(url, ".json")
        if os.path.exists(path):
            self._hits += 1
            with open(path, "r") as f:
                return json.load(f)
        self._misses += 1
        data = super().fetch_json(url, timeout)
        if data is not None:
            os.makedirs(self._cache_dir, exist_ok=True)
            with open(path, "w") as f:
                json.dump(data, f)
        return data

    @property
    def stats(self):
        return {"hits": self._hits, "misses": self._misses}
