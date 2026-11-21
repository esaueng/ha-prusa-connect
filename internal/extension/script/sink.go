package script

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/internal/extension/bus"
	"github.com/prusa3d/prusa-connect/sdk/event"
	"github.com/prusa3d/prusa-connect/sdk/eventsapi"
	"github.com/prusa3d/prusa-connect/sdk/scriptapi"
)

// The names a script's event carries for each domain fact. The webhook sink
// uses the same strings in its kind member, so the two references agree.
const (
	kindPrinterStateChanged  = "printer.state_changed"
	kindPrinterLabelsChanged = "printer.labels_changed"
	kindPrinterDeleted       = "printer.deleted"
	kindJobUpdated           = "job.updated"
	kindLabelDeleted         = "label.deleted"
)

const (
	// maxBodyBytes is the largest body this host sends. The runtime's memory
	// budget alone would let a script build something far larger than any
	// destination should receive.
	maxBodyBytes = 1 << 20

	// defaultTimeout bounds one delivery when the operator sets none.
	defaultTimeout = 5 * time.Second

	// routingKeyHeader carries the key a script computed to an HTTP
	// destination. A broker routes on the key; an HTTP endpoint reads it here.
	routingKeyHeader = "X-Prusa-Routing-Key"

	// maxSinkResponseBytes is the most of a destination's answer that is read.
	// The answer is thrown away, so reading more only costs time and traffic.
	maxSinkResponseBytes = 64 << 10
)

// ErrInvalidSettings reports a script sink's configuration block the host will
// not build from.
var ErrInvalidSettings = errors.New("script: invalid sink settings")

// settings is what an operator writes under a script instance's config block.
type settings struct {
	// Endpoints maps the name a script writes onto the destination the host
	// sends to. The script chooses the key, the operator chooses the
	// destination. An http or https address is posted to. An event: address is
	// this server's own bus, and what follows the colon is the event type the
	// host publishes.
	Endpoints map[string]string `json:"endpoints"`
	// Timeout bounds one delivery, as a Go duration such as 2s.
	Timeout string `json:"timeout"`
}

// Sink is a script tier event sink: one compiled script, and the destinations
// the operator declared for it.
//
// It satisfies eventsapi.Processor, so the supervision plane, the bounded
// queue, the breaker and the sink feed treat it as any other sink. Only the
// body is different: the script builds it and this type sends it.
type Sink struct {
	program   *Program
	endpoints map[string]destination
	client    *http.Client
	bus       bus.Emitter
	log       *slog.Logger
	now       func() time.Time
}

var _ eventsapi.Processor = (*Sink)(nil)

// NewSink compiles the script and prepares the destinations it may name.
//
// It is called from the supervisor's start, so a script that will not compile
// fails the instance instead of running and dropping every event.
//
// emitter is the local event stream this instance may publish on. A nil emitter
// means the host offers none, and an instance that declares an event: endpoint
// is then refused here, instead of logging on every event.
func NewSink(source string, config json.RawMessage, logger *slog.Logger, emitter bus.Emitter) (*Sink, error) {
	declared, err := settingsOf(config)
	if err != nil {
		return nil, err
	}

	endpoints, err := endpointsOf(declared.Endpoints, emitter != nil)
	if err != nil {
		return nil, err
	}

	timeout, err := timeoutOf(declared.Timeout)
	if err != nil {
		return nil, err
	}

	program, err := Compile(source)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	client := &http.Client{
		Timeout: timeout,
		// Redirects are not followed. A redirect could point this server at a
		// host the operator did not declare.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return &Sink{
		program:   program,
		endpoints: endpoints,
		client:    client,
		bus:       emitter,
		log:       logger,
		now:       time.Now,
	}, nil
}

// PrinterStateChanged runs the script over a printer's state change.
func (s *Sink) PrinterStateChanged(state eventsapi.PrinterState) {
	s.publish(scriptapi.Event{
		Kind:     kindPrinterStateChanged,
		DeviceID: state.ID,
		State:    state.State,
	})
}

// PrinterLabelsChanged runs the script over a printer's label change.
func (s *Sink) PrinterLabelsChanged(labels eventsapi.PrinterLabels) {
	s.publish(scriptapi.Event{
		Kind:     kindPrinterLabelsChanged,
		DeviceID: labels.ID,
		Mode:     labels.Mode,
	})
}

// PrinterDeleted runs the script over a printer being removed.
func (s *Sink) PrinterDeleted(id string) {
	s.publish(scriptapi.Event{Kind: kindPrinterDeleted, DeviceID: id})
}

// JobUpdated runs the script over a job being created or changed.
func (s *Sink) JobUpdated(job eventsapi.JobInfo) {
	s.publish(scriptapi.Event{
		Kind:      kindJobUpdated,
		DeviceID:  job.PrinterID,
		JobID:     job.ID,
		JobState:  job.State,
		JobSource: job.Source,
	})
}

// FactObserved runs the script over one durable event from the host's bus.
//
// The event type goes in as the kind. A core type such as
// com.prusa.connect.device.command.settled is already what a script branches
// on, and one shared kind for every bus fact would force every script to parse
// the type out of a field it cannot see.
//
// The payload does not reach the script. scriptapi.Event has no member for data
// whose schema belongs to whoever emitted it, and adding one would change the
// script contract and not this sink. A script sees that a fact happened, for
// which machine and when. That is enough to route on and not enough to read.
func (s *Sink) FactObserved(fact eventsapi.Fact) {
	printer := ""
	if fact.Origin.Kind() == event.KindDevice {
		printer = fact.Origin.ID()
	}

	s.publish(scriptapi.Event{Kind: fact.Type, DeviceID: printer})
}

// LabelDeleted runs the script over a label being removed.
func (s *Sink) LabelDeleted(id string) {
	s.publish(scriptapi.Event{Kind: kindLabelDeleted, LabelID: id})
}

// Close releases the idle connections the deliveries left behind.
func (s *Sink) Close() error {
	s.client.CloseIdleConnections()

	return nil
}

// publish runs the script and sends what it returned.
//
// A script that fails at run time costs this event and no more. The program
// stays compiled and the next event runs it again, so a script that fails every
// time produces log lines and not a stopped instance.
func (s *Sink) publish(fact scriptapi.Event) {
	fact.Time = s.now().UTC().Format(time.RFC3339)

	messages, err := s.program.Run(fact)
	if err != nil {
		s.log.Warn("the script refused an event", "kind", fact.Kind, "error", err.Error())

		return
	}

	for _, message := range messages {
		s.deliver(message)
	}
}

// deliver sends one message to the endpoint the operator declared under the
// name the script wrote.
func (s *Sink) deliver(message scriptapi.Message) {
	where, declared := s.endpoints[message.Endpoint]
	if !declared {
		s.log.Warn("the script named an endpoint this instance does not declare",
			"endpoint", message.Endpoint, "declared", strings.Join(endpointNames(s.endpoints), ", "))

		return
	}

	if len(message.Body) > maxBodyBytes {
		s.log.Warn("the script built a body larger than the host sends",
			"endpoint", message.Endpoint, "bytes", len(message.Body), "limit", maxBodyBytes)

		return
	}

	if where.onTheBus() {
		s.emit(message, where.eventType)

		return
	}

	s.post(message, where.address)
}

// emit publishes one message on this server's own event stream, as an event of
// the instance that emitted it.
//
// A refusal costs this event alone, the same as a failed delivery. The usual
// cause is a body that is not JSON, because the bus carries it as a data
// member. The log line says so, so the author does not have to guess from a
// destination that never answers.
func (s *Sink) emit(message scriptapi.Message, eventType string) {
	if err := s.bus.Emit(bus.Emission{
		Type: eventType,
		Key:  message.Key,
		Body: message.Body,
	}); err != nil {
		s.log.Warn("the event the script built was not published",
			"endpoint", message.Endpoint, "event_type", eventType, "error", err.Error())
	}
}

// post sends one message to the HTTP address the operator declared.
func (s *Sink) post(message scriptapi.Message, address string) {
	// The client's timeout is the only bound. The contract's methods take no
	// context, so there is none to inherit here, and a deadline per delivery is
	// what keeps a dead destination from holding the instance's queue.
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		address, bytes.NewReader([]byte(message.Body)))
	if err != nil {
		s.log.Warn("the message could not be addressed",
			"endpoint", message.Endpoint, "error", err.Error())

		return
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(routingKeyHeader, message.Key)

	response, err := s.client.Do(request)
	if err != nil {
		s.log.Warn("the destination did not answer",
			"endpoint", message.Endpoint, "error", err.Error())

		return
	}

	// The body is read to the end so the connection can be reused. Past
	// maxSinkResponseBytes the connection is not worth the read, and is closed.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSinkResponseBytes))
		_ = response.Body.Close()
	}()

	switch {
	case response.StatusCode >= http.StatusBadRequest:
		s.log.Warn("the destination refused the message",
			"endpoint", message.Endpoint, "status", response.StatusCode)
	case response.StatusCode >= http.StatusMultipleChoices:
		s.log.Warn("the destination answered with a redirect, which is not followed",
			"endpoint", message.Endpoint, "status", response.StatusCode)
	}
}

// settingsOf decodes the instance's own block and refuses a field it does not
// know. Without that, "endpoint" written for "endpoints" would be ignored and
// the operator would be told the table is empty, not that the name is wrong.
func settingsOf(config json.RawMessage) (settings, error) {
	declared := settings{}

	if len(bytes.TrimSpace(config)) == 0 {
		return settings{}, fmt.Errorf("%w: config is empty and at least one endpoint is needed",
			ErrInvalidSettings)
	}

	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&declared); err != nil {
		return settings{}, fmt.Errorf("%w: %w", ErrInvalidSettings, err)
	}

	return declared, nil
}

// endpointsOf resolves every declared destination: an absolute HTTP address, or
// the local event stream and the type published on it.
//
// hasBus says whether this host offers a bus. An event: endpoint declared on a
// host with none is refused here, at start, and not once per event.
func endpointsOf(declared map[string]string, hasBus bool) (map[string]destination, error) {
	if len(declared) == 0 {
		return nil, fmt.Errorf("%w: endpoints is empty and a script has nowhere to publish",
			ErrInvalidSettings)
	}

	out := make(map[string]destination, len(declared))

	for name, address := range declared {
		// The bus form is cut at its prefix and not parsed as a URL, so
		// everything after the colon is the event type and the type rules
		// judge all of it. url.Parse would split a query or a fragment into a
		// field of its own, and two different addresses would give one type.
		if eventType, isBus := strings.CutPrefix(address, eventScheme+":"); isBus {
			held, err := busDestination(name, eventType, hasBus)
			if err != nil {
				return nil, err
			}

			out[name] = destination{eventType: held}

			continue
		}

		parsed, err := url.Parse(address)
		if err != nil {
			return nil, fmt.Errorf("%w: endpoint %q: %w", ErrInvalidSettings, name, err)
		}

		if parsed.Scheme != httpScheme && parsed.Scheme != httpsScheme {
			return nil, fmt.Errorf(
				"%w: endpoint %q is %q, expected an http address, an https address or %s:<event type>",
				ErrInvalidSettings, name, address, eventScheme)
		}

		if parsed.Host == "" {
			return nil, fmt.Errorf("%w: endpoint %q names no host", ErrInvalidSettings, name)
		}

		out[name] = destination{address: address}
	}

	return out, nil
}

// busDestination reads the event type an event: endpoint publishes.
//
// The core event namespace is closed. Every type in it is published and gated,
// and readers route on it. A script instance that invented one would put a name
// on the wire that no vocabulary defines, so the type an operator declares here
// must sit outside that namespace.
func busDestination(name, eventType string, hasBus bool) (string, error) {
	if !hasBus {
		return "", fmt.Errorf("%w: endpoint %q publishes on the event stream and this host offers none",
			ErrInvalidSettings, name)
	}

	if err := event.ValidateTypeSyntax(eventType); err != nil {
		return "", fmt.Errorf("%w: endpoint %q: %w", ErrInvalidSettings, name, err)
	}

	if strings.HasPrefix(eventType, event.CoreNamespace) {
		return "", fmt.Errorf(
			"%w: endpoint %q publishes %q, and %s is this project's own closed namespace",
			ErrInvalidSettings, name, eventType, event.CoreNamespace)
	}

	return eventType, nil
}

// timeoutOf reads the delivery bound, and refuses one that removes it.
func timeoutOf(declared string) (time.Duration, error) {
	if strings.TrimSpace(declared) == "" {
		return defaultTimeout, nil
	}

	timeout, err := time.ParseDuration(declared)
	if err != nil {
		return 0, fmt.Errorf("%w: timeout %q: %w", ErrInvalidSettings, declared, err)
	}

	if timeout <= 0 {
		return 0, fmt.Errorf("%w: timeout is %s, and a delivery with no bound holds the queue",
			ErrInvalidSettings, timeout)
	}

	return timeout, nil
}

// endpointNames lists the endpoints this instance declares, so a refusal can
// name them.
func endpointNames(endpoints map[string]destination) []string {
	names := make([]string, 0, len(endpoints))
	for name := range endpoints {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
