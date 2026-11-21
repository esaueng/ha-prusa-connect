package queue_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/simple-queue/internal/dbtest"
	"github.com/prusa3d/simple-queue/internal/queue"
)

// onEachEngine runs one case on a fresh database of every engine the run can
// reach: the operator chooses the engine, so a store that holds on one and
// not the other is broken for somebody.
func onEachEngine(t *testing.T, run func(t *testing.T, store *queue.Store)) {
	t.Helper()

	for _, engine := range dbtest.Engines(t) {
		t.Run(string(engine.Name), func(t *testing.T) {
			t.Parallel()

			grant := engine.Grant(t)
			run(t, queue.NewStore(dbtest.Open(t, grant), grant.Engine))
		})
	}
}

// Every field of a file comes back as it was written, in both lists and in
// their order. Times come back as the same instant, to the nanosecond SQLite
// keeps as text and the microsecond Postgres keeps.
func TestAQueueSurvivesTheStore(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 12, 21, 30, 15, 123456000, time.FixedZone("CET", 3600))
	ended := at.Add(time.Hour)
	wrote := queue.Queue{
		Device: "printer/1",
		Entries: []queue.Entry{
			{
				ID: "a", Storage: "local", Path: "parts/a.bgcode", Name: "a.bgcode", AddedBy: "user:alice", AddedAt: at,
				State: queue.Sending, Changed: at.Add(time.Minute), StartedBy: "user:bob", Landing: "usb/a-a.bgcode",
				Attempts: 2, TransferID: "t-1", CommandID: "c-1", JobID: "j-1",
				Facts: queue.Facts{
					EstimatedSeconds: 7503,
					PrinterModel:     "XL2IS",
					LayerHeightMM:    0.2,
					Material:         "PLA;PETG",
					Size:             4_760_000,
					Modified:         1_795_000_000,
				},
			},
			{
				ID: "b", Path: "usb/b.gcode", Name: "b.gcode", AddedBy: "user:alice", AddedAt: at, State: queue.Waiting,
				Changed: at, Note: queue.NoteNotArrived, Detail: "it is not there", Arriving: true,
			},
		},
		History: []queue.Entry{
			{
				ID:      "z",
				Name:    "z.gcode",
				State:   queue.Ended,
				AddedAt: at,
				Changed: ended,
				Outcome: queue.Finished,
				Ended:   &ended,
			},
			{ID: "y", Name: "y.gcode", State: queue.Ended, AddedAt: at, Changed: at, Outcome: queue.Failed, Ended: &at},
		},
	}

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		if _, err := store.Update(t.Context(), wrote.Device, func(q *queue.Queue) error {
			q.Entries, q.History = wrote.Entries, wrote.History

			return nil
		}); err != nil {
			t.Fatal(err)
		}

		read, err := store.Read(t.Context(), wrote.Device)
		if err != nil {
			t.Fatal(err)
		}

		if want := inUTC(wrote); !reflect.DeepEqual(read, want) {
			t.Fatalf("the queue read back\n%+v\nand was written\n%+v", read, want)
		}

		devices, err := store.Devices(t.Context())
		if err != nil || len(devices) != 1 || devices[0] != wrote.Device {
			t.Fatalf("the store lists %v, %v", devices, err)
		}
	})
}

// inUTC is a queue with every time in UTC, which is how the store answers them.
func inUTC(q queue.Queue) queue.Queue {
	lists := [][]queue.Entry{append([]queue.Entry(nil), q.Entries...), append([]queue.Entry(nil), q.History...)}

	for _, list := range lists {
		for i := range list {
			list[i].AddedAt, list[i].Changed = list[i].AddedAt.UTC(), list[i].Changed.UTC()

			if list[i].Ended != nil {
				when := list[i].Ended.UTC()
				list[i].Ended = &when
			}
		}
	}

	q.Entries, q.History = lists[0], lists[1]

	return q
}

func TestAnUnchangedQueueIsNotWritten(t *testing.T) {
	t.Parallel()

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		if _, err := store.Update(
			t.Context(),
			"p",
			func(*queue.Queue) error { return queue.ErrUnchanged },
		); err != nil {
			t.Fatal(err)
		}

		if devices, _ := store.Devices(t.Context()); len(devices) != 0 {
			t.Fatalf("an unchanged queue wrote %v", devices)
		}
	})
}

// A queue that became empty stays empty when it is read again, and a file
// moved to another place is read in its new place.
func TestAWriteReplacesWhatTheQueueHeld(t *testing.T) {
	t.Parallel()

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		add := func(id string) {
			if _, err := store.Update(t.Context(), "p", func(q *queue.Queue) error {
				return q.Add(queue.Entry{ID: id}, 5)
			}); err != nil {
				t.Fatal(err)
			}
		}

		add("a")
		add("b")
		add("c")

		if _, err := store.Update(t.Context(), "p", func(q *queue.Queue) error { return q.Move("c", 0) }); err != nil {
			t.Fatal(err)
		}

		if read, _ := store.Read(t.Context(), "p"); ids(read.Entries) != "c a b" {
			t.Fatalf("after the move the queue reads %q", ids(read.Entries))
		}

		for _, id := range []string{"a", "b", "c"} {
			if _, err := store.Update(
				t.Context(),
				"p",
				func(q *queue.Queue) error { return q.Remove(id) },
			); err != nil {
				t.Fatal(err)
			}
		}

		if read, err := store.Read(t.Context(), "p"); err != nil || !read.Empty() {
			t.Fatalf("the emptied queue reads %+v, %v", read, err)
		}
	})
}

func ids(entries []queue.Entry) string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}

	return strings.Join(out, " ")
}

// Two writers of one queue take turns. The second waits until the first has
// written, and then decides on the queue as the first left it, so neither
// change is lost. It holds for a printer's first write too, when there is no
// row yet to wait on.
func TestASecondWriterWaitsForTheFirst(t *testing.T) {
	t.Parallel()

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		for _, first := range []bool{true, false} {
			device := fmt.Sprintf("p-%t", first)
			// A file is named by its id on every printer, so each round names its own.
			mine, theirs := "mine-"+device, "theirs-"+device

			if !first {
				if _, err := store.Update(t.Context(), device, func(q *queue.Queue) error {
					return q.Add(queue.Entry{ID: "before"}, 5)
				}); err != nil {
					t.Fatal(err)
				}
			}

			entered, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 2)
			saw := make(chan string, 1)

			go func() {
				_, err := store.Update(t.Context(), device, func(q *queue.Queue) error {
					close(entered)
					<-release

					return q.Add(queue.Entry{ID: mine}, 5)
				})
				done <- err
			}()

			<-entered

			go func() {
				_, err := store.Update(t.Context(), device, func(q *queue.Queue) error {
					saw <- ids(q.Entries)

					return q.Add(queue.Entry{ID: theirs}, 5)
				})
				done <- err
			}()

			select {
			case held := <-saw:
				t.Fatalf("the second writer did not wait, and read %q", held)
			case <-time.After(200 * time.Millisecond):
			}

			close(release)

			for range 2 {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}

			want := mine
			if !first {
				want = "before " + mine
			}

			if held := <-saw; held != want {
				t.Fatalf("the second writer read %q, want %q", held, want)
			}

			if read, _ := store.Read(t.Context(), device); ids(read.Entries) != want+" "+theirs {
				t.Fatalf("after both writers the queue reads %q", ids(read.Entries))
			}
		}
	})
}

// A person and the queue itself write at the same moment, and so do people
// on different printers. Every file is kept, once.
func TestWritersAtOnceLoseNothing(t *testing.T) {
	t.Parallel()

	const writers, printers = 6, 3

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		var wait sync.WaitGroup

		failed := make(chan error, writers*printers)

		for p := range printers {
			for w := range writers {
				wait.Go(func() {
					_, err := store.Update(t.Context(), fmt.Sprintf("p%d", p), func(q *queue.Queue) error {
						return q.Add(queue.Entry{ID: fmt.Sprintf("p%d-w%d", p, w)}, writers)
					})
					if err != nil {
						failed <- err
					}
				})
			}
		}

		wait.Wait()
		close(failed)

		for err := range failed {
			t.Fatal(err)
		}

		for p := range printers {
			read, err := store.Read(t.Context(), fmt.Sprintf("p%d", p))
			if err != nil {
				t.Fatal(err)
			}

			seen := map[string]bool{}
			for _, entry := range read.Entries {
				seen[entry.ID] = true
			}

			if len(read.Entries) != writers || len(seen) != writers {
				t.Fatalf("printer %d holds %q after %d writers", p, ids(read.Entries), writers)
			}
		}
	})
}

// The rules of the queue never leave two files on their way at once. Should
// a fault in them try, the database refuses the write and the queue stays as
// it was.
func TestTheDatabaseRefusesTwoActiveFiles(t *testing.T) {
	t.Parallel()

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		if _, err := store.Update(t.Context(), "p", func(q *queue.Queue) error {
			return q.Add(queue.Entry{ID: "a"}, 5)
		}); err != nil {
			t.Fatal(err)
		}

		_, err := store.Update(t.Context(), "p", func(q *queue.Queue) error {
			q.Entries = []queue.Entry{{ID: "a", State: queue.Sending}, {ID: "b", State: queue.Printing}}

			return nil
		})
		if !errors.Is(err, queue.ErrStore) {
			t.Fatalf("two active files were written: %v", err)
		}

		if read, _ := store.Read(t.Context(), "p"); ids(read.Entries) != "a" || read.Entries[0].State != queue.Waiting {
			t.Fatalf("after the refused write the queue reads %+v", read.Entries)
		}
	})
}

// The database keeps states, notes and outcomes as plain text, so the store
// is what refuses a word the module does not name.
func TestTheStoreRefusesAnUnknownWord(t *testing.T) {
	t.Parallel()

	for name, entry := range map[string]queue.Entry{
		"state":   {ID: "a", State: "lost"},
		"note":    {ID: "a", State: queue.Waiting, Note: "gone_fishing"},
		"outcome": {ID: "a", State: queue.Ended, Outcome: "exploded"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			onEachEngine(t, func(t *testing.T, store *queue.Store) {
				_, err := store.Update(t.Context(), "p", func(q *queue.Queue) error {
					q.Entries = []queue.Entry{entry}

					return nil
				})
				if !errors.Is(err, queue.ErrUnknownWord) {
					t.Fatalf("an unknown %s was written: %v", name, err)
				}
			})
		})
	}
}

// A file is named by its id, which no two files share, on any printer.
func TestTheDatabaseRefusesOneIDTwice(t *testing.T) {
	t.Parallel()

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		for _, device := range []string{"p", "q"} {
			_, err := store.Update(t.Context(), device, func(q *queue.Queue) error {
				return q.Add(queue.Entry{ID: "a"}, 5)
			})
			if device == "q" && !errors.Is(err, queue.ErrStore) {
				t.Fatalf("the id a was written on a second printer: %v", err)
			}
		}
	})
}

// A queue is rows of a database, one for each file, so no bound on the size
// of one record caps what a queue holds: a file with a very long path fits.
func TestALongPathFitsTheQueue(t *testing.T) {
	t.Parallel()

	onEachEngine(t, func(t *testing.T, store *queue.Store) {
		long := strings.Repeat("x", 70<<10)

		if _, err := store.Update(t.Context(), "p", func(q *queue.Queue) error {
			return q.Add(queue.Entry{ID: "a", Path: long}, 5)
		}); err != nil {
			t.Fatal(err)
		}

		if read, err := store.Read(t.Context(), "p"); err != nil || read.Entries[0].Path != long {
			t.Fatalf("the long path did not come back: %v", err)
		}
	})
}

func TestAStoreWithNoDatabaseRefuses(t *testing.T) {
	t.Parallel()

	store := &queue.Store{}

	if _, err := store.Read(t.Context(), "p"); !errors.Is(err, queue.ErrNotOpen) {
		t.Fatalf("a read with no database answered %v", err)
	}

	if _, err := store.Update(
		t.Context(),
		"p",
		func(*queue.Queue) error { return nil },
	); !errors.Is(
		err,
		queue.ErrNotOpen,
	) {
		t.Fatalf("a write with no database answered %v", err)
	}

	if _, err := store.Devices(t.Context()); !errors.Is(err, queue.ErrNotOpen) {
		t.Fatalf("a listing with no database answered %v", err)
	}
}
