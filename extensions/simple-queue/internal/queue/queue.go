// Package queue holds the print queue of one printer, and the rules for
// changing it.
//
// A queue is a list of files a person wants printed on one printer, in the
// order they print. The first entry may be on its way to the printer or
// printing. Every entry after it waits. An entry that ended moves to the
// history, newest first.
//
// The functions here change a Queue value in memory and make no call. The
// store in store.go reads and writes the value, so a rule is tested without
// a server.
package queue

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// State is where an entry is.
type State string

// The states of an entry, in the order an entry moves through them.
const (
	// Waiting is an entry that has not been sent to the printer.
	Waiting State = "waiting"
	// Sending is an entry whose file is on its way to the printer. The printer
	// opens the print when the last byte has arrived.
	Sending State = "sending"
	// Printing is an entry the printer is printing.
	Printing State = "printing"
	// Ended is an entry whose print stopped. Outcome says how.
	Ended State = "ended"
)

// Known reports whether s is one of the states above. The database keeps
// states, notes and outcomes as plain text, so the store refuses any other.
func (s State) Known() bool {
	switch s {
	case Waiting, Sending, Printing, Ended:
		return true
	}

	return false
}

// Outcome is how a print ended, in the words the server publishes for every
// printer.
type Outcome string

// The outcomes of a print.
const (
	Finished  Outcome = "finished"
	Failed    Outcome = "failed"
	Cancelled Outcome = "cancelled"
	Unknown   Outcome = "unknown"
)

// Known reports whether o is one of the outcomes above, or none for a print
// that has not ended.
func (o Outcome) Known() bool {
	switch o {
	case "", Finished, Failed, Cancelled, Unknown:
		return true
	}

	return false
}

// Note says why a waiting entry did not start. A person reads it beside the
// entry. It is cleared when the entry starts.
type Note string

// The notes on a waiting entry.
const (
	// NoteSendFailed is a print the server refused to start.
	NoteSendFailed Note = "send_failed"
	// NoteTransferFailed is a file that did not reach the printer.
	NoteTransferFailed Note = "transfer_failed"
	// NoteNotStarted is a file the printer did not open in time.
	NoteNotStarted Note = "not_started"
	// NoteOwnerMayNot is an entry whose author may no longer print on this
	// printer or read the file. The queue does not start it by itself.
	NoteOwnerMayNot Note = "owner_may_not"
	// NoteOwnerUnknown is an entry whose author the server cannot answer
	// about, so the queue does not start it by itself.
	NoteOwnerUnknown Note = "owner_unknown"
	// NoteOwnerUnchecked is an entry whose author the server will not answer
	// about: on a server that keeps no list of people, a person this process
	// has not served since it started. It waits for a person to start it.
	NoteOwnerUnchecked Note = "owner_unchecked"
	// NotePrinterNotFree is a file that was to start on a printer that was
	// printing, paused, away or in any other state no print starts from. The
	// detail is that state. The note goes once the printer is at rest.
	NotePrinterNotFree Note = "printer_not_free"
	// NoteNoStorage is a printer that reports no storage the file can be
	// written to.
	NoteNoStorage Note = "no_storage"
	// NoteTakenBack is a file a person stopped on its way to the printer. It
	// waits for a person to start it again.
	NoteTakenBack Note = "taken_back"
	// NoteNotArrived is a file that was added while it was being copied to
	// the printer and is not there. It is asked about again on every start.
	NoteNotArrived Note = "not_arrived"
)

// Known reports whether n is one of the notes above, or none.
func (n Note) Known() bool {
	switch n {
	case "", NoteSendFailed, NoteTransferFailed, NoteNotStarted, NoteOwnerMayNot, NoteOwnerUnknown,
		NoteOwnerUnchecked, NotePrinterNotFree, NoteNoStorage, NoteTakenBack, NoteNotArrived:
		return true
	}

	return false
}

// Facts is what the slicer wrote about a file, and its size, read when the
// file was added. A zero field is a file that does not say, or one nobody
// could ask about.
type Facts struct {
	// EstimatedSeconds is how long the file prints, in whole seconds.
	EstimatedSeconds int64 `json:"estimated_seconds,omitempty"`
	// PrinterModel is the printer the file was sliced for, such as XL2IS.
	PrinterModel string `json:"printer_model,omitempty"`
	// LayerHeightMM is the layer height in millimetres.
	LayerHeightMM float64 `json:"layer_height_mm,omitempty"`
	// Material is the material, one for each tool joined by a semicolon.
	Material string `json:"material,omitempty"`
	// Size is the file's size in bytes.
	Size int64 `json:"size,omitempty"`
	// Modified is when a file on the printer was last written, in seconds
	// since the Unix epoch. With the size it names the version of the file.
	Modified int64 `json:"modified,omitempty"`
}

// Entry is one file on the queue.
type Entry struct {
	// Facts are what the slicer wrote about the file, and its size.
	Facts

	ID string `json:"id"`

	// Storage and Path name the file in one of this server's storages. An
	// empty Storage is a file already on the printer, and Path is its path
	// there, such as usb/bracket.bgcode.
	Storage string `json:"storage"`
	Path    string `json:"path"`
	// Name is what a person reads: the file's name without its folders.
	Name string `json:"name"`

	// AddedBy is the principal of the person who put the file on the queue.
	AddedBy string    `json:"added_by"`
	AddedAt time.Time `json:"added_at"`

	State   State     `json:"state"`
	Changed time.Time `json:"changed_at"`

	// Note and Detail say why a waiting entry did not start. Detail is the
	// server's or the printer's own words, when there are any.
	Note   Note   `json:"note,omitempty"`
	Detail string `json:"detail,omitempty"`

	// StartedBy is who started the print: a person's principal, or empty for
	// the queue itself.
	StartedBy string `json:"started_by,omitempty"`
	// Landing is where the file was written on the printer, such as
	// usb/bracket.bgcode. Attempts counts the sends.
	Landing  string `json:"landing,omitempty"`
	Attempts int    `json:"attempts,omitempty"`

	// Arriving marks a file on the printer that was added while this server
	// was still copying it there. Nothing has seen it on the printer yet, so a
	// start looks for it first and clears the mark once it is there.
	Arriving bool `json:"arriving,omitempty"`

	TransferID string `json:"transfer_id,omitempty"`
	CommandID  string `json:"command_id,omitempty"`
	// JobID is the server's id of the print, once the printer opened it.
	JobID string `json:"job_id,omitempty"`

	Outcome Outcome    `json:"outcome,omitempty"`
	Ended   *time.Time `json:"ended_at,omitempty"`
}

// Active reports whether the entry is on its way to the printer or printing.
func (e Entry) Active() bool { return e.State == Sending || e.State == Printing }

// Queue is the queue of one printer.
type Queue struct {
	Device string `json:"device_id"`
	// Entries is the active entry first, if there is one, and then the
	// waiting entries in the order they print.
	Entries []Entry `json:"entries"`
	// History is the entries that ended, newest first.
	History []Entry `json:"history,omitempty"`
}

// The refusals of the rules below.
var (
	// ErrNoEntry is an id the queue does not hold.
	ErrNoEntry = errors.New("queue: no such entry")
	// ErrFull is a queue that holds as many waiting entries as it may.
	ErrFull = errors.New("queue: the queue is full")
	// ErrActive is an entry that is on its way to the printer or printing.
	ErrActive = errors.New("queue: the entry is on its way to the printer or printing")
	// ErrBusy is a printer that already has an active entry.
	ErrBusy = errors.New("queue: the printer already has a print from the queue")
	// ErrEmpty is a queue with nothing waiting.
	ErrEmpty = errors.New("queue: nothing is waiting")
)

// Active answers the active entry, or nil when there is none.
func (q *Queue) Active() *Entry {
	if len(q.Entries) > 0 && q.Entries[0].Active() {
		return &q.Entries[0]
	}

	return nil
}

// Next answers the entry that prints next, or nil when nothing waits.
func (q *Queue) Next() *Entry {
	for index := range q.Entries {
		if q.Entries[index].State == Waiting {
			return &q.Entries[index]
		}
	}

	return nil
}

// WaitingCount answers how many entries wait.
func (q *Queue) WaitingCount() int {
	count := 0

	for _, entry := range q.Entries {
		if entry.State == Waiting {
			count++
		}
	}

	return count
}

// Empty reports whether the queue holds nothing, in the list or the history.
func (q *Queue) Empty() bool { return len(q.Entries) == 0 && len(q.History) == 0 }

// Add puts an entry at the end of the queue. limit is how many entries may
// wait.
func (q *Queue) Add(entry Entry, limit int) error {
	if q.WaitingCount() >= limit {
		return fmt.Errorf("%w: %d entries wait and the limit is %d", ErrFull, q.WaitingCount(), limit)
	}

	entry.State = Waiting
	entry.Changed = entry.AddedAt
	q.Entries = append(q.Entries, entry)

	return nil
}

// Remove takes a waiting entry off the queue, or an ended one out of the
// history.
func (q *Queue) Remove(id string) error {
	if index := q.index(id); index >= 0 {
		if q.Entries[index].Active() {
			return ErrActive
		}

		q.Entries = slices.Delete(q.Entries, index, index+1)

		return nil
	}

	if index := slices.IndexFunc(q.History, func(e Entry) bool { return e.ID == id }); index >= 0 {
		q.History = slices.Delete(q.History, index, index+1)

		return nil
	}

	return ErrNoEntry
}

// Move puts a waiting entry at a place among the waiting entries. 0 is the
// next one to print, and a place past the end is the end.
func (q *Queue) Move(id string, place int) error {
	index := q.index(id)
	if index < 0 {
		return ErrNoEntry
	}

	if q.Entries[index].Active() {
		return ErrActive
	}

	entry := q.Entries[index]
	q.Entries = slices.Delete(q.Entries, index, index+1)

	first := 0
	if q.Active() != nil {
		first = 1
	}

	at := min(first+max(place, 0), len(q.Entries))
	q.Entries = slices.Insert(q.Entries, at, entry)

	return nil
}

// ClearHistory forgets every ended entry.
func (q *Queue) ClearHistory() { q.History = nil }

// Begin marks the next entry as sent, before the file is sent. by is who
// starts it, and empty for the queue. landing is where the file is written on
// the printer.
//
// It is written to the store before the print is asked for, so two copies of
// this module never send one entry twice.
func (q *Queue) Begin(id, by, landing string, now time.Time) error {
	if q.Active() != nil {
		return ErrBusy
	}

	next := q.Next()
	if next == nil {
		return ErrEmpty
	}

	if next.ID != id {
		return fmt.Errorf("%w: the next entry is %s", ErrNoEntry, next.ID)
	}

	index := q.index(id)
	entry := q.Entries[index]
	entry.State = Sending
	entry.Changed = now
	entry.Note = ""
	entry.Detail = ""
	entry.StartedBy = by
	entry.Landing = landing
	entry.Attempts++
	entry.TransferID = ""
	entry.CommandID = ""
	entry.JobID = ""

	q.Entries = slices.Delete(q.Entries, index, index+1)
	q.Entries = slices.Insert(q.Entries, 0, entry)

	return nil
}

// Sent records the ids the server answered the print with, and landing when
// the server wrote the file under another name than the one asked for.
func (q *Queue) Sent(id, transfer, command, landing string) error {
	active := q.Active()
	if active == nil || active.ID != id {
		return ErrNoEntry
	}

	active.TransferID = transfer
	active.CommandID = command

	if landing != "" {
		active.Landing = landing
	}

	return nil
}

// GiveBack puts an entry that was sent back at the head of the queue, with
// the reason it did not print.
func (q *Queue) GiveBack(id string, note Note, detail string, now time.Time) error {
	active := q.Active()
	if active == nil || active.ID != id {
		return ErrNoEntry
	}

	active.State = Waiting
	active.Changed = now
	active.Note = note
	active.Detail = clip(detail)
	active.TransferID = ""
	active.CommandID = ""
	active.JobID = ""

	return nil
}

// Hold writes why the next entry does not start by itself, and leaves it
// waiting. It answers false when nothing changed.
func (q *Queue) Hold(id string, note Note, detail string) bool {
	index := q.index(id)
	if index < 0 || q.Entries[index].State != Waiting {
		return false
	}

	detail = clip(detail)
	if q.Entries[index].Note == note && q.Entries[index].Detail == detail {
		return false
	}

	q.Entries[index].Note = note
	q.Entries[index].Detail = detail

	return true
}

// Arrived records that a file added on its way to the printer is there now.
func (q *Queue) Arrived(id string) bool {
	index := q.index(id)
	if index < 0 || !q.Entries[index].Arriving {
		return false
	}

	q.Entries[index].Arriving = false
	if q.Entries[index].Note == NoteNotArrived || q.Entries[index].Note == NoteTransferFailed {
		q.Entries[index].Note = ""
		q.Entries[index].Detail = ""
	}

	return true
}

// CopyEnded records how the copy of a file queued on its way to the printer
// ended. landing is where the copy wrote, as the server names it. A copy that
// landed makes the entry a file on the printer. One that did not is written
// on the entry, which stays queued so a person sees it. It answers false
// when no entry waits for that copy.
func (q *Queue) CopyEnded(landing string, landed bool, detail string) bool {
	landing = strings.TrimPrefix(landing, "/")

	for index := range q.Entries {
		entry := &q.Entries[index]
		if !entry.Arriving || entry.State != Waiting || strings.TrimPrefix(entry.Path, "/") != landing {
			continue
		}

		if landed {
			return q.Arrived(entry.ID)
		}

		return q.Hold(entry.ID, NoteTransferFailed, detail)
	}

	return false
}

// Learned records what a waiting file says about itself, once somebody could
// ask. A fact the entry already holds stays. It answers false when the entry
// is gone or nothing new was learned.
func (q *Queue) Learned(id string, facts Facts) bool {
	index := q.index(id)
	if index < 0 {
		return false
	}

	held := &q.Entries[index].Facts
	before := *held
	if held.EstimatedSeconds == 0 {
		held.EstimatedSeconds = facts.EstimatedSeconds
	}
	if held.PrinterModel == "" {
		held.PrinterModel = facts.PrinterModel
	}
	if held.LayerHeightMM == 0 {
		held.LayerHeightMM = facts.LayerHeightMM
	}
	if held.Material == "" {
		held.Material = facts.Material
	}
	if held.Size == 0 {
		held.Size = facts.Size
	}
	if held.Modified == 0 {
		held.Modified = facts.Modified
	}

	return *held != before
}

// Started records that the printer opened a print. It answers true when the
// print is the file the queue sent, which is matched by its name: a printer
// may report the path in another spelling, but the name is the one the queue
// gave the file.
func (q *Queue) Started(job, printed string, now time.Time) bool {
	active := q.Active()
	if active == nil || active.State != Sending || !SameFile(active.Landing, printed) {
		return false
	}

	active.State = Printing
	active.Changed = now
	active.JobID = job

	return true
}

// Settled records that a print stopped. It answers true when the print is the
// active entry's: the same job id, or, for an entry whose job id the queue
// never heard, the same file. keep is how many ended entries the history
// keeps.
func (q *Queue) Settled(job, printed string, outcome Outcome, ended time.Time, keep int) bool {
	active := q.Active()
	if active == nil {
		return false
	}

	switch {
	case active.JobID != "" && active.JobID == job:
	case active.JobID == "" && SameFile(active.Landing, printed):
	default:
		return false
	}

	active.JobID = job
	q.end(outcome, ended, keep)

	return true
}

// end moves the active entry into the history.
func (q *Queue) end(outcome Outcome, ended time.Time, keep int) {
	entry := q.Entries[0]
	entry.State = Ended
	entry.Changed = ended
	entry.Outcome = outcome
	entry.Ended = &ended

	q.Entries = q.Entries[1:]
	q.History = slices.Insert(q.History, 0, entry)
	q.Keep(keep)
}

// Keep trims the history to keep entries.
func (q *Queue) Keep(keep int) {
	if len(q.History) > keep {
		q.History = q.History[:keep]
	}
}

// index answers where an entry is in the list, or -1.
func (q *Queue) index(id string) int {
	return slices.IndexFunc(q.Entries, func(e Entry) bool { return e.ID == id })
}

// SameFile reports whether the printer's path names the file the queue wrote.
// The storage is FAT, where one name in two spellings is one file, so the
// names are compared without case.
func SameFile(sent, printed string) bool {
	return sent != "" && printed != "" && strings.EqualFold(path.Base(sent), path.Base(printed))
}

// MaxDetailBytes bounds the words kept with a note, so one long refusal does
// not take the room of many entries.
const MaxDetailBytes = 240

// clip cuts a text to MaxDetailBytes on a character boundary.
func clip(text string) string {
	if len(text) <= MaxDetailBytes {
		return text
	}

	cut := MaxDetailBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}

	return text[:cut]
}

// OnPrinter reports whether the entry is a file already on the printer, which
// is printed where it is and never sent.
func (e Entry) OnPrinter() bool { return e.Storage == "" }
