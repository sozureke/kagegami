-- A pipeline is tier 2's plan for the whole map, from `kagegami pipeline`. Each run is kept, so
-- a later run can be compared with an earlier one; nothing here is ever rewritten.
CREATE TABLE pipeline_run (
    id         INTEGER PRIMARY KEY,
    created_at INTEGER NOT NULL,
    model      TEXT    NOT NULL,
    summary    TEXT    NOT NULL,
    steps      TEXT    NOT NULL -- JSON array of advice.Step, as it was shown to the tester
) STRICT;
