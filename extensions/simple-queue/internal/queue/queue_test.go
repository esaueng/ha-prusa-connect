package queue

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

// filled answers a queue with one waiting entry per id, in that order.
func filled(t *testing.T, ids ...string) Queue {
	t.Helper()

	var q Queue

	for _, id := range ids {
		if err := q.Add(Entry{ID: id, Path: id + ".bgcode", Name: id + ".bgcode", AddedAt: at}, 10); err != nil {
			t.Fatal(err)
		}
	}

	return q
}

func order(q Queue) string {
	ids := make([]string, 0, len(q.Entries))
	for _, entry := range q.Entries {
		ids = append(ids, entry.ID)
	}

	return strings.Join(ids, ",")
}

func TestAQueueRefusesAnEntryOverItsLimit(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b")

	if err := q.Add(Entry{ID: "c"}, 2); !errors.Is(err, ErrFull) {
		t.Fatalf("a third entry on a queue of two answered %v", err)
	}
}

func TestMovingPutsAWaitingEntryAtItsPlace(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b", "c")

	if err := q.Move("c", 0); err != nil {
		t.Fatal(err)
	}

	if got := order(q); got != "c,a,b" {
		t.Fatalf("moving c to the front gave %s", got)
	}

	if err := q.Move("c", 99); err != nil {
		t.Fatal(err)
	}

	if got := order(q); got != "a,b,c" {
		t.Fatalf("moving c past the end gave %s", got)
	}
}

func TestTheActiveEntryStaysFirstAndCannotBeMovedOrRemoved(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b", "c")

	if err := q.Begin("a", "", "usb/a.bgcode", at); err != nil {
		t.Fatal(err)
	}

	if err := q.Move("c", 0); err != nil {
		t.Fatal(err)
	}

	if got := order(q); got != "a,c,b" {
		t.Fatalf("moving c to the front of a printing queue gave %s", got)
	}

	if err := q.Move("a", 2); !errors.Is(err, ErrActive) {
		t.Fatalf("moving the active entry answered %v", err)
	}

	if err := q.Remove("a"); !errors.Is(err, ErrActive) {
		t.Fatalf("removing the active entry answered %v", err)
	}
}

func TestBeginTakesOnlyTheNextEntryAndOnlyOnce(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b")

	if err := q.Begin("b", "", "usb/b.bgcode", at); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("beginning the second entry answered %v", err)
	}

	if err := q.Begin("a", "user:alice", "usb/a.bgcode", at); err != nil {
		t.Fatal(err)
	}

	if err := q.Begin("b", "", "usb/b.bgcode", at); !errors.Is(err, ErrBusy) {
		t.Fatalf("beginning while one is active answered %v", err)
	}

	active := q.Active()
	if active.State != Sending || active.StartedBy != "user:alice" || active.Attempts != 1 {
		t.Fatalf("the begun entry reads %+v", active)
	}
}

func TestAPrintIsFollowedFromSendToHistory(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b")

	if err := q.Begin("a", "", "usb/a.bgcode", at); err != nil {
		t.Fatal(err)
	}

	if q.Started("job-1", "/usb/OTHER.bgcode", at) {
		t.Fatal("a print of another file was taken for the queue's")
	}

	if !q.Started("job-1", "/usb/A.BGCODE", at) {
		t.Fatal("the print of the sent file in another spelling was not recognised")
	}

	if q.Settled("job-2", "usb/a.bgcode", Finished, at, 5) {
		t.Fatal("another job settled the queue's print")
	}

	if !q.Settled("job-1", "", Finished, at.Add(time.Hour), 5) {
		t.Fatal("the queue's job did not settle")
	}

	if q.Active() != nil || order(q) != "b" || len(q.History) != 1 {
		t.Fatalf("after the print the queue reads %s with %d in history", order(q), len(q.History))
	}

	if ended := q.History[0]; ended.Outcome != Finished || ended.Ended == nil || !ended.Ended.Equal(at.Add(time.Hour)) {
		t.Fatalf("the history reads %+v", ended)
	}
}

func TestAnEntryGivenBackWaitsAtTheHeadWithItsReason(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b")

	if err := q.Begin("a", "", "usb/a.bgcode", at); err != nil {
		t.Fatal(err)
	}

	if err := q.Sent("a", "transfer", "command", ""); err != nil {
		t.Fatal(err)
	}

	if err := q.GiveBack("a", NoteSendFailed, strings.Repeat("é", MaxDetailBytes), at); err != nil {
		t.Fatal(err)
	}

	next := q.Next()
	if next.ID != "a" || next.Note != NoteSendFailed || next.TransferID != "" || next.Attempts != 1 {
		t.Fatalf("the entry given back reads %+v", next)
	}

	if len(next.Detail) > MaxDetailBytes || !strings.HasPrefix(strings.Repeat("é", MaxDetailBytes), next.Detail) {
		t.Fatalf("the detail was not cut on a character: %d bytes", len(next.Detail))
	}
}

func TestTheHistoryKeepsTheNewestOnly(t *testing.T) {
	t.Parallel()

	q := filled(t, "a", "b", "c")

	for _, id := range []string{"a", "b", "c"} {
		if err := q.Begin(id, "", "usb/"+id+".bgcode", at); err != nil {
			t.Fatal(err)
		}

		q.end(Cancelled, at, 2)
	}

	if len(q.History) != 2 || q.History[0].ID != "c" || q.History[1].ID != "b" {
		t.Fatalf("the history reads %+v", q.History)
	}

	if err := q.Remove("b"); err != nil || len(q.History) != 1 {
		t.Fatalf("removing from the history answered %v and left %d", err, len(q.History))
	}
}

func TestHoldWritesOnlyAChange(t *testing.T) {
	t.Parallel()

	q := filled(t, "a")

	if !q.Hold("a", NoteOwnerMayNot, "operate") {
		t.Fatal("the first note was not written")
	}

	if q.Hold("a", NoteOwnerMayNot, "operate") {
		t.Fatal("the same note was written twice")
	}
}
