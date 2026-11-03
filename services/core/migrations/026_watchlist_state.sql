CREATE TABLE watchlist_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision BETWEEN 0 AND 9007199254740991),
    state_json TEXT NOT NULL CHECK (length(state_json) <= 16384),
    state_sha256 TEXT NOT NULL CHECK (length(state_sha256) = 64)
);

CREATE TRIGGER watchlist_state_no_delete BEFORE DELETE ON watchlist_state
BEGIN
    SELECT RAISE(ABORT, 'watchlist singleton must be preserved');
END;

CREATE TRIGGER watchlist_state_update_guard BEFORE UPDATE ON watchlist_state
WHEN NEW.singleton != OLD.singleton OR NEW.revision != OLD.revision + 1
BEGIN
    SELECT RAISE(ABORT, 'watchlist revision must advance once');
END;

INSERT INTO watchlist_state(singleton,revision,state_json,state_sha256)
VALUES(1,0,'{"revision":0,"updated_at":null,"entries":[]}', 'ebe3680d9edf19cb6190a0f1482280921ed943efff3d7911eb91453049ba5a1b');

CREATE TRIGGER watchlist_state_no_insert BEFORE INSERT ON watchlist_state
BEGIN
    SELECT RAISE(ABORT, 'watchlist singleton is already initialized');
END;
