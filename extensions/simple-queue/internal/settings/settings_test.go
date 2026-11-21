package settings_test

import (
	"errors"
	"testing"
	"time"

	"github.com/prusa3d/simple-queue/internal/settings"
)

func TestReadTakesTheDefaults(t *testing.T) {
	t.Parallel()

	values, err := settings.Read(settings.Sent{
		MaxWaiting: settings.DefaultMaxWaiting, KeepHistory: settings.DefaultKeepHistory, SendTimeout: "2h15m",
	})
	if err != nil || values != settings.Defaults() {
		t.Fatalf("the defaults read as %+v, %v", values, err)
	}
}

func TestReadRefusesAValueOutOfItsRange(t *testing.T) {
	t.Parallel()

	for name, sent := range map[string]settings.Sent{
		"no waiting":      {MaxWaiting: 0, KeepHistory: 1, SendTimeout: "1h"},
		"too many":        {MaxWaiting: settings.MaxMaxWaiting + 1, KeepHistory: 1, SendTimeout: "1h"},
		"long history":    {MaxWaiting: 1, KeepHistory: settings.MaxKeepHistory + 1, SendTimeout: "1h"},
		"short timeout":   {MaxWaiting: 1, KeepHistory: 1, SendTimeout: "1m"},
		"no duration":     {MaxWaiting: 1, KeepHistory: 1, SendTimeout: "soon"},
		"endless timeout": {MaxWaiting: 1, KeepHistory: 1, SendTimeout: (25 * time.Hour).String()},
	} {
		if _, err := settings.Read(sent); !errors.Is(err, settings.ErrOutOfRange) {
			t.Errorf("%s answered %v", name, err)
		}
	}
}

func TestLiveAnswersTheDefaultsUntilSet(t *testing.T) {
	t.Parallel()

	var live settings.Live

	if live.Ready() || live.Get() != settings.Defaults() {
		t.Fatal("an empty Live is ready or answers other than the defaults")
	}

	live.Set(settings.Values{MaxWaiting: 2})

	if !live.Ready() || live.Get().MaxWaiting != 2 {
		t.Fatal("Live does not answer what was set")
	}
}
