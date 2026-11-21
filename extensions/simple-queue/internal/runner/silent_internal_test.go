package runner

import (
	"testing"

	"github.com/prusa3d/simple-queue/internal/queue"
)

// A mark lasts as long as its entry: one that left the queue is dropped, and
// another printer's marks stay.
func TestForgetSilentDropsTheMarksOfEntriesThatLeft(t *testing.T) {
	t.Parallel()

	var r Runner
	r.silent.Store("p1\x00kept", struct{}{})
	r.silent.Store("p1\x00gone", struct{}{})
	r.silent.Store("p2\x00gone", struct{}{})
	r.unheard.Store("p1\x00gone", struct{}{})

	r.forgetSilent("p1", queue.Queue{Entries: []queue.Entry{{ID: "kept"}}})

	for key, want := range map[string]bool{"p1\x00kept": true, "p1\x00gone": false, "p2\x00gone": true} {
		if _, held := r.silent.Load(key); held != want {
			t.Errorf("%q is held: %v, want %v", key, held, want)
		}
	}

	if _, held := r.unheard.Load("p1\x00gone"); held {
		t.Error("the unheard mark of an entry that left is held")
	}
}
