-- Notes of note.v1.NoteService. Shared by every backend language, so the
-- column types are the engine's natural ones.
CREATE TABLE IF NOT EXISTS notes (
    -- COLLATE "C": ids order bytewise, like in every other backend and language.
    id          text COLLATE "C" PRIMARY KEY,
    owner       text           NOT NULL,
    title       text           NOT NULL,
    body        text,
    labels      text[],
    etag        text           NOT NULL,
    create_time timestamptz(6) NOT NULL,
    update_time timestamptz(6) NOT NULL
);

-- Serves ListNotes: one owner's notes, newest first, id as the tiebreaker.
CREATE INDEX IF NOT EXISTS notes_owner_list_idx ON notes (owner, create_time DESC, id DESC);
