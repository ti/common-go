-- Table of the conformance suite (dbtest.Table). utf8mb4_bin: the default
-- MySQL collation compares case-insensitively, the other engines do not.
CREATE TABLE IF NOT EXISTS dbtest_items (
    id          varchar(64) COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    owner       varchar(64) COLLATE utf8mb4_bin NOT NULL,
    title       varchar(255) NOT NULL DEFAULT '',
    body        text,
    labels      json,
    `rank`      bigint NOT NULL DEFAULT 0,
    etag        varchar(64) COLLATE utf8mb4_bin,
    create_time datetime(6) NOT NULL,
    KEY dbtest_items_owner_idx (owner, create_time DESC, id DESC)
);
