-- What the models cost: one row per call, with the tokens the provider counted. No prompt and no
-- answer is kept here.
CREATE TABLE model_usage (
    id                INTEGER PRIMARY KEY,
    ts                INTEGER NOT NULL,
    tier              TEXT    NOT NULL CHECK (tier IN ('tier1', 'tier2')),
    model             TEXT    NOT NULL,
    prompt_tokens     INTEGER NOT NULL CHECK (prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL CHECK (completion_tokens >= 0),
    ok                INTEGER NOT NULL CHECK (ok IN (0, 1))
) STRICT;

CREATE INDEX idx_model_usage_tier ON model_usage(tier, model);
