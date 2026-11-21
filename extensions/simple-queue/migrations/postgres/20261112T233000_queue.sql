-- The queue of each printer, one row for each file.

-- One row for each printer that has, or had, a queue. A write of a printer's
-- queue writes this row first, which makes a second writer of the same queue
-- wait for the first. version counts those writes.
CREATE TABLE queue_printer (
    device_id TEXT    NOT NULL PRIMARY KEY,
    version   BIGINT  NOT NULL
);

-- A file on a queue, named by its id. history is 0 for a file that waits, is
-- on its way or prints, and 1 for one that ended. position orders each of the
-- two lists: the queue in the order it prints, the history newest first.
-- state is one of waiting, sending, printing and ended; the module checks it,
-- so a new state needs no rebuild of this table.
--
-- storage is empty for a file already on the printer, and path is then its
-- path there. arriving is 1 for such a file while it is still being copied to
-- the printer. started_by is empty for a print the queue started by itself.
CREATE TABLE queue_entry (
    device_id   TEXT    NOT NULL REFERENCES queue_printer (device_id),
    history     INTEGER NOT NULL CHECK (history IN (0, 1)),
    position    INTEGER NOT NULL,
    id          TEXT    NOT NULL PRIMARY KEY,
    storage     TEXT    NOT NULL,
    path        TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    added_by    TEXT    NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL,
    state       TEXT    NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL,
    note        TEXT    NOT NULL DEFAULT '',
    detail      TEXT    NOT NULL DEFAULT '',
    started_by  TEXT    NOT NULL DEFAULT '',
    landing     TEXT    NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    arriving    INTEGER NOT NULL DEFAULT 0 CHECK (arriving IN (0, 1)),
    transfer_id TEXT    NOT NULL DEFAULT '',
    command_id  TEXT    NOT NULL DEFAULT '',
    job_id      TEXT    NOT NULL DEFAULT '',
    outcome     TEXT    NOT NULL DEFAULT '',
    ended_at    TIMESTAMPTZ NULL,
    -- What the file says of itself, read when it was added: what the slicer
    -- wrote, its size in bytes, and for a file on the printer when it was last
    -- written in seconds since the Unix epoch. 0 and '' are a file that does
    -- not say.
    file_estimated_seconds BIGINT NOT NULL DEFAULT 0,
    file_printer_model     TEXT NOT NULL DEFAULT '',
    file_layer_height_mm   DOUBLE PRECISION NOT NULL DEFAULT 0,
    file_material          TEXT NOT NULL DEFAULT '',
    file_size              BIGINT NOT NULL DEFAULT 0,
    file_modified          BIGINT NOT NULL DEFAULT 0,
    UNIQUE (device_id, history, position)
);

-- A printer takes one file from its queue at a time. The module's rules hold
-- that, and this holds it against a fault in them.
CREATE UNIQUE INDEX queue_one_active ON queue_entry (device_id) WHERE state IN ('sending', 'printing');
