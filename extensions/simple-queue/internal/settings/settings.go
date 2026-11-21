// Package settings holds the values an operator may set for this module, with
// their defaults and their ranges.
//
// The defaults are written twice, here and in module.yaml, because the host
// reads the manifest and the code reads this file. manifest_test.go fails when
// the two differ.
package settings

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// The defaults and the ranges of each setting.
//
// A page reads a printer's whole queue every few seconds, one row for each
// file, so the waiting entries and the history together stay under 90.
const (
	DefaultMaxWaiting = 30
	MinMaxWaiting     = 1
	MaxMaxWaiting     = 60

	DefaultKeepHistory = 10
	MinKeepHistory     = 0
	MaxKeepHistory     = 30

	DefaultSendTimeout = 2*time.Hour + 15*time.Minute
	MinSendTimeout     = 5 * time.Minute
	MaxSendTimeout     = 24 * time.Hour
)

// ErrOutOfRange is a value outside the range a setting publishes.
var ErrOutOfRange = errors.New("settings: the value is outside its range")

// Sent is what the host sends on plugin.configure: the operator's config
// block merged over the defaults of module.yaml. A duration arrives as the
// text an operator wrote, such as 2h15m.
type Sent struct {
	MaxWaiting  int    `json:"max_waiting"`
	KeepHistory int    `json:"keep_history"`
	SendTimeout string `json:"send_timeout"`
}

// Values are the settings in the shape the module uses.
type Values struct {
	// MaxWaiting is how many entries may wait on one printer's queue.
	MaxWaiting int
	// KeepHistory is how many ended entries each queue keeps.
	KeepHistory int
	// SendTimeout is how long an entry may stay on its way to the printer
	// before the queue gives it back.
	SendTimeout time.Duration
}

// Defaults are the values of a module nobody configured.
func Defaults() Values {
	return Values{
		MaxWaiting:  DefaultMaxWaiting,
		KeepHistory: DefaultKeepHistory,
		SendTimeout: DefaultSendTimeout,
	}
}

// Read checks what the host sent and answers it as Values. A value outside its
// range is refused, and the sentence names the key and the range, because the
// operator reads it on the instance.
func Read(sent Sent) (Values, error) {
	timeout, err := time.ParseDuration(sent.SendTimeout)
	if err != nil {
		return Values{}, fmt.Errorf("%w: send_timeout reads %q, and it is a duration such as 2h15m",
			ErrOutOfRange, sent.SendTimeout)
	}

	switch {
	case sent.MaxWaiting < MinMaxWaiting || sent.MaxWaiting > MaxMaxWaiting:
		return Values{}, fmt.Errorf("%w: max_waiting is %d and it is from %d to %d",
			ErrOutOfRange, sent.MaxWaiting, MinMaxWaiting, MaxMaxWaiting)
	case sent.KeepHistory < MinKeepHistory || sent.KeepHistory > MaxKeepHistory:
		return Values{}, fmt.Errorf("%w: keep_history is %d and it is from %d to %d",
			ErrOutOfRange, sent.KeepHistory, MinKeepHistory, MaxKeepHistory)
	case timeout < MinSendTimeout || timeout > MaxSendTimeout:
		return Values{}, fmt.Errorf("%w: send_timeout is %s and it is from %s to %s",
			ErrOutOfRange, timeout, MinSendTimeout, MaxSendTimeout)
	}

	return Values{MaxWaiting: sent.MaxWaiting, KeepHistory: sent.KeepHistory, SendTimeout: timeout}, nil
}

// Live holds the values the host sent last. The host sends them before any
// request, and again when an operator changes them.
type Live struct {
	mu     sync.RWMutex
	values Values
	set    bool
}

// Set replaces the values.
func (l *Live) Set(values Values) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.values = values
	l.set = true
}

// Get answers the values, and the defaults before the host sent any.
func (l *Live) Get() Values {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.set {
		return Defaults()
	}

	return l.values
}

// Ready reports whether the host has sent the settings.
func (l *Live) Ready() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.set
}
