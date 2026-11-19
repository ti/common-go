-- Notes of note.v1.NoteService (MySQL 8.0.17+: JSON arrays, MEMBER OF).
-- The default MySQL collation is case-insensitive: without utf8mb4_bin the
-- owner "Alice" could read the notes of "alice", and etags would match
-- regardless of case.
CREATE TABLE IF NOT EXISTS notes (
    id          varchar(64)  COLLATE utf8mb4_bin NOT NULL PRIMARY KEY,
    owner       varchar(255) COLLATE utf8mb4_bin NOT NULL,
    title       varchar(255) NOT NULL,
    body        text,
    labels      json,
    etag        varchar(64)  COLLATE utf8mb4_bin NOT NULL,
    create_time datetime(6)  NOT NULL,
    update_time datetime(6)  NOT NULL,
    KEY notes_owner_list_idx (owner, create_time DESC, id DESC)
);
