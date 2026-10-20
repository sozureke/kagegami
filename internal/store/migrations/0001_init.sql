-- Schema version 1. All timestamps are UTC Unix milliseconds (ADR-0004).

CREATE TABLE engagement (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    name        TEXT    NOT NULL,
    target      TEXT    NOT NULL DEFAULT '',
    goal        TEXT    NOT NULL DEFAULT '',
    scope       TEXT    NOT NULL DEFAULT '',
    roe         TEXT    NOT NULL DEFAULT '',
    phase       TEXT    NOT NULL DEFAULT 'recon',
    difficulty  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
) STRICT;

CREATE TABLE output_blob (
    id          INTEGER PRIMARY KEY,
    cmd         TEXT    NOT NULL,
    exit_code   INTEGER,
    path        TEXT    NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
    sha256      TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL,
    finished_at INTEGER
) STRICT;

CREATE TABLE host (
    id             INTEGER PRIMARY KEY,
    ip             TEXT    NOT NULL UNIQUE,
    hostname       TEXT    NOT NULL DEFAULT '',
    os             TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL DEFAULT 'unknown',
    first_seen     INTEGER NOT NULL,
    last_seen      INTEGER NOT NULL,
    source_blob_id INTEGER REFERENCES output_blob(id) ON DELETE SET NULL
) STRICT;

CREATE TABLE service (
    id             INTEGER PRIMARY KEY,
    host_id        INTEGER NOT NULL REFERENCES host(id) ON DELETE CASCADE,
    port           INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    proto          TEXT    NOT NULL CHECK (proto IN ('tcp', 'udp')),
    name           TEXT    NOT NULL DEFAULT '',
    version        TEXT    NOT NULL DEFAULT '',
    state          TEXT    NOT NULL DEFAULT 'open',
    source_blob_id INTEGER REFERENCES output_blob(id) ON DELETE SET NULL,
    UNIQUE (host_id, port, proto)
) STRICT;

CREATE TABLE web_path (
    id             INTEGER PRIMARY KEY,
    service_id     INTEGER NOT NULL REFERENCES service(id) ON DELETE CASCADE,
    path           TEXT    NOT NULL,
    status_code    INTEGER,
    note           TEXT    NOT NULL DEFAULT '',
    source_blob_id INTEGER REFERENCES output_blob(id) ON DELETE SET NULL,
    UNIQUE (service_id, path)
) STRICT;

-- credential.secret is sensitive: it must never reach logs or output
CREATE TABLE credential (
    id             INTEGER PRIMARY KEY,
    username       TEXT    NOT NULL DEFAULT '',
    secret         TEXT    NOT NULL DEFAULT '',
    kind           TEXT    NOT NULL DEFAULT 'password',
    works_on       TEXT    NOT NULL DEFAULT '',
    source_blob_id INTEGER REFERENCES output_blob(id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE TABLE finding (
    id             INTEGER PRIMARY KEY,
    severity       TEXT    NOT NULL CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    title          TEXT    NOT NULL,
    description    TEXT    NOT NULL DEFAULT '',
    source_blob_id INTEGER REFERENCES output_blob(id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE TABLE journal_entry (
    id             INTEGER PRIMARY KEY,
    ts             INTEGER NOT NULL,
    author         TEXT    NOT NULL CHECK (author IN ('human', 'ai')),
    text           TEXT    NOT NULL,
    source_blob_id INTEGER REFERENCES output_blob(id) ON DELETE SET NULL
) STRICT;

CREATE INDEX idx_journal_entry_ts ON journal_entry(ts);
CREATE INDEX idx_service_host ON service(host_id);