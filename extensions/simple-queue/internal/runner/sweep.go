package runner

import (
	"context"
	"errors"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"

	"github.com/prusa3d/simple-queue/internal/queue"
)

// startSkew is how much earlier than the send a print may have started by the
// server's clock and still be the queue's. The two times come from one
// server, so this only absorbs rounding.
const startSkew = time.Minute

// Run sweeps the queues and follows the event stream until ctx ends.
//
// The sweep and the events are one loop, so the queue reads and writes each
// printer from one place, and a sweep never acts on an entry an event is
// moving.
func (r *Runner) Run(ctx context.Context, feed Feed) error {
	events := make(chan news, eventBuffer)

	go r.follow(ctx, feed, events)

	tick := time.NewTicker(SweepInterval)
	defer tick.Stop()

	r.Sweep(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the caller's own context.
		case <-tick.C:
			r.Sweep(ctx)
		case device := <-r.wakeOne:
			r.sweepOne(ctx, device)
		case arrived, open := <-events:
			if !open {
				<-ctx.Done()

				return ctx.Err() //nolint:wrapcheck // the caller's own context.
			}

			r.apply(ctx, arrived)
		}
	}
}

// Sweep looks at every printer that has a queue once.
func (r *Runner) Sweep(ctx context.Context) {
	devices, err := r.store.Devices(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			r.log("simple-queue: the queues could not be listed: %v\n", err)
		}

		return
	}

	for _, device := range devices {
		if ctx.Err() != nil {
			return
		}

		r.sweepOne(ctx, device)
	}
}

// sweepOne looks at one printer: what became of the print the queue sent, and
// whether a ready printer can take the next file.
func (r *Runner) sweepOne(ctx context.Context, device string) {
	held, err := r.store.Read(ctx, device)
	if err != nil {
		r.log("simple-queue: the queue of %s could not be read: %v\n", device, err)

		return
	}

	if active := held.Active(); active != nil {
		r.reconcile(ctx, device, *active)

		return
	}

	next := held.Next()
	if next == nil || !startsAlone(*next) {
		return
	}

	printer, err := r.Look(ctx, "", device)
	if err != nil {
		return
	}

	// The printer that kept this file from starting is free now.
	if next.Note == queue.NotePrinterNotFree && busyAs(printer) == "" {
		r.hold(ctx, device, next.ID, "", "")
	}

	if !printer.Ready {
		return
	}

	if !r.MayStartAlone(ctx, device) {
		return
	}

	if note, detail := r.owner(ctx, device, *next); note != "" {
		r.hold(ctx, device, next.ID, note, detail)

		return
	}

	// Only the entry whose owner was checked starts. A file still on its way
	// to the printer is written on its entry, and is nothing to tell the
	// operator on every sweep.
	if _, err = r.Start(ctx, device, "", next.ID); err != nil && !errors.Is(err, queue.ErrBusy) &&
		!errors.Is(err, ErrNoPrinterFile) {
		r.log("simple-queue: the next file of %s did not start: %v\n", device, err)
	}
}

// startsAlone reports whether the queue may send an entry by itself. An entry
// whose send failed waits for a person: the same send would most likely fail
// again.
func startsAlone(entry queue.Entry) bool {
	switch entry.Note {
	case queue.NoteSendFailed, queue.NoteTransferFailed, queue.NoteNotStarted, queue.NoteTakenBack,
		queue.NoteOwnerUnchecked:
		return false
	case queue.NoteOwnerMayNot, queue.NoteOwnerUnknown, queue.NoteNoStorage, queue.NoteNotArrived,
		queue.NotePrinterNotFree:
		// Each is asked again on the next sweep, and may have changed.
		return true
	}

	return true
}

// owner asks the server whether the person who added an entry may still print
// on the printer and, for a file in a storage, read the file. The queue starts
// the file as itself, and the person may have lost the right since they added
// it. It answers the note to hold the entry with, or an empty note when the
// entry may start.
//
// A server that keeps no list of people, one with an OpenID Connect login or
// in open mode, answers from the person's roles on this server, and only
// about a person this process served since it started. About anybody else it
// does not answer, and the entry waits for a person to start it. Their own
// rights are checked when they do.
func (r *Runner) owner(ctx context.Context, device string, entry queue.Entry) (queue.Note, string) {
	questions := []accessapi.CheckParams{
		{Subject: entry.AddedBy, Action: AskOperate, ObjectType: ObjectDevice, ObjectID: device},
	}
	if !entry.OnPrinter() {
		questions = append(questions, accessapi.CheckParams{
			Subject: entry.AddedBy, Action: AskReadStorage, ObjectType: ObjectKind, ObjectID: KindStorage,
		})
	}

	for _, question := range questions {
		decision, refused := r.access.Check(ctx, question)

		switch {
		case errors.Is(refused, accessapi.ErrUnknownSubject):
			return queue.NoteOwnerUnchecked, ""
		case refused != nil:
			return queue.NoteOwnerUnknown, refused.Error()
		case !decision.Allowed:
			return queue.NoteOwnerMayNot, ""
		}
	}

	return "", ""
}

// reconcile asks the printer what it is printing, and moves the active entry
// when the events did not. An event can be missed: the stream may have been
// closed, or this process was not running when the print ended.
func (r *Runner) reconcile(ctx context.Context, device string, active queue.Entry) {
	values := r.settings()
	now := r.now()

	late := active.State == queue.Sending && now.Sub(active.Changed) > values.SendTimeout

	jobs, err := r.fleet.Jobs(ctx, deviceapi.JobsParams{ID: device})
	if err != nil {
		// A send does not wait past its time for a printer nobody can ask
		// about: the entry would stay active, and nothing removes an active
		// entry.
		if late {
			r.notStarted(ctx, device, active.ID, now)
		}

		return
	}

	switch active.State {
	case queue.Sending:
		r.reconcileSending(ctx, device, active, jobs, late, values.KeepHistory)
	case queue.Printing:
		if current := jobs.Current; current != nil && current.ID == active.JobID {
			return
		}

		outcome, ended := queue.Unknown, now
		if last := jobs.Last; last != nil && last.ID == active.JobID {
			if last.Ended == nil {
				return
			}

			outcome, ended = outcomeOf(last.Outcome), *last.Ended
		}

		r.update(ctx, device, func(q *queue.Queue) bool {
			return q.Settled(active.JobID, active.Landing, outcome, ended, values.KeepHistory)
		})
	case queue.Waiting, queue.Ended:
	}
}

// sentPrint reports whether the print the printer runs is the file the queue
// sent: it has the file's name, and began no earlier than the send.
func sentPrint(active queue.Entry, current deviceapi.Job) bool {
	return !current.Started.Before(active.Changed.Add(-startSkew)) &&
		(queue.SameFile(active.Landing, current.Path) || queue.SameFile(active.Landing, current.DisplayName))
}

// reconcileSending moves an entry the queue sent by what the printer says: to
// printing when it prints the file, to the history when it already printed it
// unseen, and back to the queue when the send's time passed with neither.
func (r *Runner) reconcileSending(
	ctx context.Context, device string, active queue.Entry, jobs deviceapi.Jobs, late bool, keep int,
) {
	now := r.now()

	if current := jobs.Current; current != nil && sentPrint(active, *current) {
		r.update(ctx, device, func(q *queue.Queue) bool {
			// The print began when the printer says, which the page draws it from.
			return q.Started(current.ID, current.Path, current.Started) ||
				q.Started(current.ID, current.DisplayName, current.Started)
		})

		return
	}

	if last := jobs.Last; last != nil && last.Ended != nil && sentPrint(active, *last) {
		r.printedUnseen(ctx, device, *last, keep)

		return
	}

	if late {
		r.notStarted(ctx, device, active.ID, now)
	}
}

// printedUnseen records a print of the sent file that started and ended while
// no event reached the queue, such as while this process was not running. The
// entry goes to the history with how the print ended, and is not sent again.
func (r *Runner) printedUnseen(ctx context.Context, device string, last deviceapi.Job, keep int) {
	r.update(ctx, device, func(q *queue.Queue) bool {
		if !q.Started(last.ID, last.Path, last.Started) && !q.Started(last.ID, last.DisplayName, last.Started) {
			return false
		}

		return q.Settled(last.ID, "", outcomeOf(last.Outcome), *last.Ended, keep)
	})
}

// notStarted gives back an entry whose send the printer never opened.
func (r *Runner) notStarted(ctx context.Context, device, id string, now time.Time) {
	r.update(ctx, device, func(q *queue.Queue) bool {
		return q.GiveBack(id, queue.NoteNotStarted, "", now) == nil
	})
}

// update writes one change to a printer's queue, and logs a write that
// failed. change answers false when it changed nothing.
func (r *Runner) update(ctx context.Context, device string, change func(*queue.Queue) bool) {
	_, err := r.store.Update(ctx, device, func(q *queue.Queue) error {
		if !change(q) {
			return queue.ErrUnchanged
		}

		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		r.log("simple-queue: the queue of %s could not be written: %v\n", device, err)
	}
}
