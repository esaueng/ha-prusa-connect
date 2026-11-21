package queue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// ErrUnchanged is what a change function answers when it changed nothing, so
// Update writes nothing.
var ErrUnchanged = errors.New("queue: nothing changed")

// ErrStore is a database that refused a read or a write.
var ErrStore = errors.New("queue: the database refused the call")

// ErrUnknownWord is an entry with a state, a note or an outcome the module
// does not name, which the store refuses to write.
var ErrUnknownWord = errors.New("queue: an entry holds a state, note or outcome the module does not name")

// ErrNotOpen is a store asked before the host granted its database.
var ErrNotOpen = fmt.Errorf("%w: the database is not open yet", ErrStore)

// Store reads and writes the queues in the module's own database: one row
// for each file, and one row for each printer, which a write locks so that
// two writers of one queue take turns. The schema is migrations/, which the
// main package applies.
//
// A store is built before the host grants the database, because the routes
// hold it from the start. Use hands it the database once it is open.
type Store struct {
	held atomic.Pointer[opened]
}

// opened is a migrated database and the engine it runs on.
type opened struct {
	db     *sql.DB
	engine wirerpc.DatabaseEngine
}

// NewStore answers a store over an open, migrated database.
func NewStore(db *sql.DB, engine wirerpc.DatabaseEngine) *Store {
	store := &Store{}
	store.Use(db, engine)

	return store
}

// Use hands the store an open, migrated database.
func (s *Store) Use(db *sql.DB, engine wirerpc.DatabaseEngine) {
	s.held.Store(&opened{db: db, engine: engine})
}

// Read answers the queue of one printer. A printer nobody queued for has an
// empty queue.
func (s *Store) Read(ctx context.Context, device string) (Queue, error) {
	on := s.held.Load()
	if on == nil {
		return Queue{}, ErrNotOpen
	}

	return read(ctx, on.db, device)
}

// Update reads one printer's queue, hands it to change and writes what change
// left, all in one transaction. The transaction locks the printer's row
// first, so a second writer of the same queue waits for this one and then
// reads the queue as this one left it. Writers of other printers' queues do
// not wait on Postgres. SQLite has one writer at a time in all.
//
// change runs inside the transaction: it decides on the queue in memory and
// must not call the store or wait for anything.
//
// A change that answers ErrUnchanged writes nothing, and Update answers the
// queue it read and no error. Any other error from change is answered as it
// is, and nothing is written.
func (s *Store) Update(ctx context.Context, device string, change func(*Queue) error) (Queue, error) {
	on := s.held.Load()
	if on == nil {
		return Queue{}, ErrNotOpen
	}

	tx, err := on.db.BeginTx(ctx, nil)
	if err != nil {
		return Queue{}, fmt.Errorf("%w: write the queue of %s: %w", ErrStore, device, err)
	}
	defer tx.Rollback() //nolint:errcheck // after a commit it answers ErrTxDone, which is the expected end.

	if err = lock(ctx, tx, device); err != nil {
		return Queue{}, err
	}

	held, err := read(ctx, tx, device)
	if err != nil {
		return Queue{}, err
	}

	if err = change(&held); err != nil {
		if errors.Is(err, ErrUnchanged) {
			// The rollback also takes back the lock's own write, so a queue
			// nobody changed leaves no trace.
			return held, nil
		}

		return Queue{}, err
	}

	if err = on.write(ctx, tx, device, held); err != nil {
		return Queue{}, err
	}

	if err = tx.Commit(); err != nil {
		return Queue{}, fmt.Errorf("%w: write the queue of %s: %w", ErrStore, device, err)
	}

	return held, nil
}

// lock makes this transaction the one writer of a printer's queue until it
// ends. The row is made if the printer has none, and then written, which is
// what holds it on both engines: Postgres locks the row, and SQLite the
// database. version counts the writes and is read by nobody.
func lock(ctx context.Context, tx *sql.Tx, device string) error {
	for _, statement := range []string{
		`INSERT INTO queue_printer (device_id, version) VALUES ($1, 0) ON CONFLICT (device_id) DO NOTHING`,
		`UPDATE queue_printer SET version = version + 1 WHERE device_id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, statement, device); err != nil {
			return fmt.Errorf("%w: lock the queue of %s: %w", ErrStore, device, err)
		}
	}

	return nil
}

// Devices answers every printer that has a queue or a history, or had one.
func (s *Store) Devices(ctx context.Context) ([]string, error) {
	on := s.held.Load()
	if on == nil {
		return nil, ErrNotOpen
	}

	rows, err := on.db.QueryContext(ctx, `SELECT device_id FROM queue_printer ORDER BY device_id`)
	if err != nil {
		return nil, fmt.Errorf("%w: list the queues: %w", ErrStore, err)
	}
	defer rows.Close() // A read: rows.Err below says what went wrong.

	devices := []string{}

	for rows.Next() {
		var device string
		if err = rows.Scan(&device); err != nil {
			return nil, fmt.Errorf("%w: list the queues: %w", ErrStore, err)
		}

		devices = append(devices, device)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: list the queues: %w", ErrStore, err)
	}

	return devices, nil
}

const entryColumns = `history, id, storage, path, name, added_by, added_at, state, changed_at, note, detail,
	started_by, landing, attempts, arriving, transfer_id, command_id, job_id, outcome, ended_at,
	file_estimated_seconds, file_printer_model, file_layer_height_mm, file_material, file_size, file_modified`

// reader is the part of a database and of a transaction a read uses.
type reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// read answers the queue of one printer.
func read(ctx context.Context, from reader, device string) (Queue, error) {
	rows, err := from.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM queue_entry WHERE device_id = $1 ORDER BY history, position`, device)
	if err != nil {
		return Queue{}, fmt.Errorf("%w: read the queue of %s: %w", ErrStore, device, err)
	}
	defer rows.Close() // A read: rows.Err below says what went wrong.

	queue := Queue{Device: device}

	for rows.Next() {
		entry, history, scanErr := scanEntry(rows)
		if scanErr != nil {
			return Queue{}, fmt.Errorf("%w: the queue of %s does not read: %w", ErrStore, device, scanErr)
		}

		if history {
			queue.History = append(queue.History, entry)
		} else {
			queue.Entries = append(queue.Entries, entry)
		}
	}

	if err = rows.Err(); err != nil {
		return Queue{}, fmt.Errorf("%w: read the queue of %s: %w", ErrStore, device, err)
	}

	return queue, nil
}

// scanEntry reads one row of entryColumns.
func scanEntry(rows *sql.Rows) (Entry, bool, error) {
	var (
		entry                 Entry
		history, arriving     int
		added, changed, ended any
	)

	err := rows.Scan(&history, &entry.ID, &entry.Storage, &entry.Path, &entry.Name, &entry.AddedBy, &added,
		&entry.State, &changed, &entry.Note, &entry.Detail, &entry.StartedBy, &entry.Landing, &entry.Attempts,
		&arriving, &entry.TransferID, &entry.CommandID, &entry.JobID, &entry.Outcome, &ended, &entry.EstimatedSeconds,
		&entry.PrinterModel, &entry.LayerHeightMM, &entry.Material, &entry.Size, &entry.Modified)
	if err != nil {
		return Entry{}, false, err //nolint:wrapcheck // the caller names the queue.
	}

	entry.Arriving = arriving != 0

	if entry.AddedAt, err = parseTime(added); err != nil {
		return Entry{}, false, err
	}

	if entry.Changed, err = parseTime(changed); err != nil {
		return Entry{}, false, err
	}

	if ended != nil {
		when, parseErr := parseTime(ended)
		if parseErr != nil {
			return Entry{}, false, parseErr
		}

		entry.Ended = &when
	}

	return entry, history != 0, nil
}

// write replaces the files of one printer's queue with the ones the queue
// holds now. The files are written whole: a queue holds a few dozen, and the
// order of most of them changes when one is moved or ends.
func (o *opened) write(ctx context.Context, tx *sql.Tx, device string, queue Queue) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM queue_entry WHERE device_id = $1`, device); err != nil {
		return fmt.Errorf("%w: write the queue of %s: %w", ErrStore, device, err)
	}

	for history, list := range [][]Entry{queue.Entries, queue.History} {
		for position, entry := range list {
			if err := o.insert(ctx, tx, device, history, position, entry); err != nil {
				return fmt.Errorf("%w: write %s of the queue of %s: %w", ErrStore, entry.ID, device, err)
			}
		}
	}

	return nil
}

func (o *opened) insert(ctx context.Context, tx *sql.Tx, device string, history, position int, entry Entry) error {
	if !entry.State.Known() || !entry.Note.Known() || !entry.Outcome.Known() {
		return fmt.Errorf("%w: state %q, note %q, outcome %q", ErrUnknownWord, entry.State, entry.Note, entry.Outcome)
	}

	arriving := 0
	if entry.Arriving {
		arriving = 1
	}

	var ended any
	if entry.Ended != nil {
		ended = o.at(*entry.Ended)
	}

	_, err := tx.ExecContext(ctx, `INSERT INTO queue_entry (device_id, position, `+entryColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23,
		$24, $25, $26, $27, $28)`,
		device, position, history, entry.ID, entry.Storage, entry.Path, entry.Name, entry.AddedBy, o.at(entry.AddedAt),
		string(entry.State), o.at(entry.Changed), string(entry.Note), entry.Detail, entry.StartedBy, entry.Landing,
		entry.Attempts, arriving, entry.TransferID, entry.CommandID, entry.JobID, string(entry.Outcome), ended,
		entry.EstimatedSeconds, entry.PrinterModel, entry.LayerHeightMM, entry.Material, entry.Size, entry.Modified)

	return err //nolint:wrapcheck // the caller names the entry and the queue.
}

// textTime keeps every fraction digit, so SQLite orders the text as time.
const textTime = "2006-01-02T15:04:05.000000000Z07:00"

// at renders a time for one statement: Postgres has a type for it, and
// SQLite keeps it as text.
func (o *opened) at(when time.Time) any {
	if o.engine == wirerpc.DatabasePostgres {
		return when.UTC()
	}

	return when.UTC().Format(textTime)
}

// errTime is a time column that holds neither of what the two engines answer.
var errTime = errors.New("a time column holds no time")

// parseTime reads a time either engine answered.
func parseTime(held any) (time.Time, error) {
	switch held := held.(type) {
	case time.Time:
		return held.UTC(), nil
	case string:
		return time.Parse(time.RFC3339Nano, held) //nolint:wrapcheck // the caller names the queue.
	case []byte:
		return time.Parse(time.RFC3339Nano, string(held)) //nolint:wrapcheck // the caller names the queue.
	}

	return time.Time{}, fmt.Errorf("%w: %T", errTime, held)
}

// NewID answers a fresh entry id.
func NewID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("queue: read random bytes for an entry id: %w", err)
	}

	return hex.EncodeToString(raw[:]), nil
}
