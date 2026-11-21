// Package webhook posts domain events to an HTTP endpoint as JSON.
//
// It is a reference implementation of sdk/eventsapi, meant to be copied.
// Change the deliver method for another transport.
//
// A Processor must not block, because the caller is the event bus. Events go
// into a bounded queue that one sender goroutine drains. A full queue drops
// and counts, and Close reports what never arrived.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/eventsapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
)

// Type is the `events.type` that selects this adapter.
const Type = "webhook"

const (
	defaultQueueDepth = 256
	defaultTimeout    = 5 * time.Second
	// shutdownGrace is how long Close lets the queue drain. A failed POST is
	// never retried: a retry would reorder events behind a slow endpoint.
	shutdownGrace = 2 * time.Second
)

// ErrMissingURL rejects a spec with no endpoint.
var ErrMissingURL = errors.New("webhook: config.url is required")

//nolint:gochecknoinits // registration has to happen before main; the host reaches it through a blank import.
func init() { eventsapi.Register(Type, doc(), New) }

// doc is this sink's entry in the published extension reference. The kinds
// come from kinds.go, the table the sink publishes from, so the reference
// cannot differ from the wire. They are declared as destinations.
func doc() extension.Doc {
	return extension.Doc{
		Summary: "Posts each domain event to one HTTP endpoint as a JSON object of kind and data.",
		Description: "The simplest outbound sink there is: one endpoint, one POST per event, no retry. " +
			"A sink that retried would reorder events behind a slow endpoint and deepen the queue it is " +
			"already failing to drain, so the host's supervisor owns retry policy and this adapter drops " +
			"what it cannot deliver.",
		Version:   "1.1.0",
		Contract:  eventsapi.Contract,
		Stability: extension.StabilityStable,
		Publishes: destinations(),
		Options: []extension.Option{
			{
				Key:      "url",
				Type:     extension.OptionURL,
				Required: true,
				Summary:  "The endpoint each event is posted to.",
			},
			{
				Key:     "queue_depth",
				Type:    extension.OptionInt,
				Default: "256",
				Summary: "How many events may wait for a slow endpoint before the oldest are dropped.",
			},
			{
				Key:     "timeout",
				Type:    extension.OptionDuration,
				Default: "5s",
				Summary: "How long one POST waits before it is abandoned.",
			},
			{
				Key:  "header",
				Type: extension.OptionSecret,
				Summary: "Extra headers sent with every POST, as a block of name and value. It is a " +
					"credential because an authorization token is what it is for, so a read of the " +
					"instance answers a marker in place of the whole block and the names in it.",
			},
		},
	}
}

// Config is what the operator writes under `events.config`.
type Config struct {
	URL        string
	QueueDepth int
	Timeout    time.Duration
	Header     map[string]string
}

// event is the wire shape. Listeners depend on the field names. A field whose
// meaning changes gets a new name.
type event struct {
	Kind string `json:"kind"`
	Data any    `json:"data"`
}

// Processor implements eventsapi.Processor.
type Processor struct {
	client  *http.Client
	url     string
	header  map[string]string
	queue   chan event
	done    chan struct{}
	stopped chan struct{}

	dropped atomic.Uint64
	failed  atomic.Uint64

	closeOnce sync.Once
}

// New builds a Processor from its spec.
func New(spec eventsapi.ProviderSpec) (eventsapi.Processor, error) {
	cfg, err := configFrom(spec.Config)
	if err != nil {
		return nil, err
	}

	// The sender has its own transport. Other code may call
	// CloseIdleConnections on the default transport, and that can fail a POST
	// the endpoint has already received.
	transport := &http.Transport{}
	if shared, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = shared.Clone()
	}

	p := &Processor{
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
			// Redirects are not followed. A redirect could point this server,
			// and the headers the operator set, at a host the operator did not
			// name. A 3xx answer counts as a failed delivery.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		url:     cfg.URL,
		header:  cfg.Header,
		queue:   make(chan event, cfg.QueueDepth),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}

	go p.run()

	return p, nil
}

func configFrom(raw map[string]any) (Config, error) {
	cfg := Config{QueueDepth: defaultQueueDepth, Timeout: defaultTimeout}

	url, _ := raw["url"].(string)
	if url == "" {
		return Config{}, ErrMissingURL
	}

	cfg.URL = url

	// A number from YAML is an int or a float64, depending on the decoder.
	switch depth := raw["queue_depth"].(type) {
	case int:
		cfg.QueueDepth = depth
	case float64:
		cfg.QueueDepth = int(depth)
	}

	if cfg.QueueDepth < 1 {
		return Config{}, fmt.Errorf("webhook: queue_depth must be at least 1, got %d", cfg.QueueDepth)
	}

	if timeout, ok := raw["timeout"].(string); ok {
		parsed, err := time.ParseDuration(timeout)
		if err != nil {
			return Config{}, fmt.Errorf("webhook: config.timeout: %w", err)
		}

		cfg.Timeout = parsed
	}

	if header, ok := raw["header"].(map[string]any); ok {
		cfg.Header = make(map[string]string, len(header))
		for k, v := range header {
			if s, isString := v.(string); isString {
				cfg.Header[k] = s
			}
		}
	}

	return cfg, nil
}

func (p *Processor) PrinterStateChanged(e eventsapi.PrinterState) {
	p.offer(event{Kind: PrinterStateChanged.Name, Data: e})
}

func (p *Processor) PrinterLabelsChanged(e eventsapi.PrinterLabels) {
	p.offer(event{Kind: PrinterLabelsChanged.Name, Data: e})
}

func (p *Processor) PrinterDeleted(id string) {
	p.offer(event{Kind: PrinterDeleted.Name, Data: map[string]string{"id": id}})
}

func (p *Processor) JobUpdated(e eventsapi.JobInfo) {
	p.offer(event{Kind: JobUpdated.Name, Data: e})
}

func (p *Processor) LabelDeleted(id string) {
	p.offer(event{Kind: LabelDeleted.Name, Data: map[string]string{"id": id}})
}

// FactObserved posts one durable bus event, with its whole envelope as the
// data.
func (p *Processor) FactObserved(fact eventsapi.Fact) {
	p.offer(event{Kind: FactObserved.Name, Data: fact})
}

// offer never blocks, because the caller is the event bus. When the queue is
// full the event is dropped and counted.
func (p *Processor) offer(e event) {
	select {
	case <-p.done:
		p.dropped.Add(1)
	case p.queue <- e:
	default:
		p.dropped.Add(1)
	}
}

// run is the only goroutine that touches the network, so the endpoint sees
// events in the order they were offered.
func (p *Processor) run() {
	defer close(p.stopped)

	for {
		select {
		case <-p.done:
			p.drain()

			return
		case e := <-p.queue:
			p.deliver(e)
		}
	}
}

// drain posts what is queued, for at most shutdownGrace. It does not
// guarantee delivery.
func (p *Processor) drain() {
	deadline := time.NewTimer(shutdownGrace)
	defer deadline.Stop()

	for {
		select {
		case <-deadline.C:
			p.dropped.Add(uint64(len(p.queue)))

			return
		case e := <-p.queue:
			p.deliver(e)
		default:
			return
		}
	}
}

// deliver is the method to replace when copying this adapter.
func (p *Processor) deliver(e event) {
	body, err := json.Marshal(e)
	if err != nil {
		p.failed.Add(1)

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.client.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		p.failed.Add(1)

		return
	}

	req.Header.Set("Content-Type", "application/json")

	for k, v := range p.header {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.failed.Add(1)

		return
	}

	// The body is closed unread.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		p.failed.Add(1)
	}
}

// Close stops the sender and reports what never arrived. It is safe to call
// more than once.
func (p *Processor) Close() error {
	p.closeOnce.Do(func() { close(p.done) })

	<-p.stopped
	p.client.CloseIdleConnections()

	dropped, failed := p.dropped.Load(), p.failed.Load()
	if dropped == 0 && failed == 0 {
		return nil
	}

	return fmt.Errorf("webhook: %d event(s) dropped, %d delivery attempt(s) failed", dropped, failed)
}
