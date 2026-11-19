-- Table of the conformance suite (dbtest.Table).
CREATE TABLE IF NOT EXISTS dbtest_items (
    id          text COLLATE "C" PRIMARY KEY,
    owner       text NOT NULL,
    title       text NOT NULL DEFAULT '',
    body        text,
    labels      text[],
    rank        bigint NOT NULL DEFAULT 0,
    etag        text,
    create_time timestamptz(6) NOT NULL
);
CREATE INDEX IF NOT EXISTS dbtest_items_owner_idx ON dbtest_items (owner, create_time DESC, id DESC);
