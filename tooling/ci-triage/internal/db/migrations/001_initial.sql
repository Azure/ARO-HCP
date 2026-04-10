CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT OR IGNORE INTO schema_version (version) VALUES (1);

CREATE TABLE IF NOT EXISTS jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    env         TEXT NOT NULL,
    job_type    TEXT NOT NULL,
    job_name    TEXT NOT NULL,
    build_id    TEXT NOT NULL UNIQUE,
    base_url    TEXT NOT NULL,
    revision    TEXT,
    pr_number   INTEGER,
    state       TEXT NOT NULL,
    started_at  TEXT,
    finished_at TEXT,
    ingested_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS test_results (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id                      INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    test_name                   TEXT NOT NULL,
    status                      TEXT NOT NULL,
    failure_message             TEXT,
    failure_message_normalized  TEXT
);

CREATE TABLE IF NOT EXISTS build_log_cache (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    step        TEXT NOT NULL,
    container   TEXT NOT NULL,
    total_lines INTEGER,
    cache_path  TEXT NOT NULL,
    fetched_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_env_type       ON jobs(env, job_type);
CREATE INDEX IF NOT EXISTS idx_jobs_started        ON jobs(started_at);
CREATE INDEX IF NOT EXISTS idx_jobs_pr             ON jobs(pr_number) WHERE pr_number IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_results_job         ON test_results(job_id);
CREATE INDEX IF NOT EXISTS idx_results_name        ON test_results(test_name);
CREATE INDEX IF NOT EXISTS idx_results_name_status ON test_results(test_name, status);
