package event

import (
	"fmt"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/vocabulary"
)

// The published event-type reference. The content version changes when a type
// is added, retired or redefined. The format version changes when the shape of
// an entry changes.
const (
	// VocabularyName is what the published index and the API path call this set.
	VocabularyName = "event-types"
	// VocabularyVersion is the content version of the core event-type set.
	VocabularyVersion = "3.9.0"
	// VocabularyFormat is the shape of a published event-type entry.
	VocabularyFormat = "1.0"

	// CoreNamespace is the prefix this project owns. The hub refuses a type
	// under it that is not in the vocabulary. A type outside it is checked for
	// syntax only.
	CoreNamespace = "com.prusa.connect."

	// maxTypeLen bounds a type name, so it fits a messaging subject and an SSE
	// event field.
	maxTypeLen = 128

	minTypeSegments = 2
	typeSeparator   = "."
)

// The core event types. Each is reverse-DNS and past tense.
const (
	// TypeDeviceArrived reports a device the host has taken and registered.
	TypeDeviceArrived = CoreNamespace + "device.arrived"
	// TypeDeviceDeparted reports a device whose connection ended.
	TypeDeviceDeparted = CoreNamespace + "device.departed"
	// TypeDevicePresenceChanged reports a device becoming unreachable, or
	// reachable again, without its session ending.
	TypeDevicePresenceChanged = CoreNamespace + "device.presence.changed"
	// TypeDeviceLinkChanged reports what this server is making of the frames
	// one device sends.
	TypeDeviceLinkChanged = CoreNamespace + "device.link.changed"
	// TypeDeviceIssuesChanged reports what the host has found wrong with one
	// device changing.
	TypeDeviceIssuesChanged = CoreNamespace + "device.issues.changed"
	// TypeDeviceStateChanged reports the word a device reports for what it
	// is doing changing, such as READY to PRINTING.
	TypeDeviceStateChanged = CoreNamespace + "device.state.changed"
	// TypeDeviceTelemetrySampled reports one measurement of a device.
	TypeDeviceTelemetrySampled = CoreNamespace + "device.telemetry.sampled"
	// TypeDeviceRecordChanged reports the stored record of one device changing.
	TypeDeviceRecordChanged = CoreNamespace + "device.record.changed"
	// TypeDeviceStoragesChanged reports the storage devices one device
	// reported changing, such as a drive put in or pulled out.
	TypeDeviceStoragesChanged = CoreNamespace + "device.storages.changed"
	// TypeDeviceFilesChanged reports a read of one device folder beginning or
	// landing, or this server's copy of that folder changing.
	TypeDeviceFilesChanged = CoreNamespace + "device.files.changed"
	// TypeDeviceCamerasChanged reports the cameras listed for one device
	// changing: a camera bound to it, taken off it, or changed.
	TypeDeviceCamerasChanged = CoreNamespace + "device.cameras.changed"
	// TypeCameraFrameReceived reports a camera that was handed a new frame.
	TypeCameraFrameReceived = CoreNamespace + "camera.frame.received"
	// TypeResourceStateChanged reports a configured instance changing state.
	TypeResourceStateChanged = CoreNamespace + "resource.state.changed"
	// TypeResourceUIChanged reports a running module changing which entries
	// of its own interface it hides.
	TypeResourceUIChanged = CoreNamespace + "resource.ui.changed"
	// TypeStreamContinuityLost reports a reader that cannot be resumed without
	// a gap.
	TypeStreamContinuityLost = CoreNamespace + "stream.continuity.lost"
	// TypeStreamOriginWithdrawn reports an origin a reader followed leaving
	// what that reader may see.
	TypeStreamOriginWithdrawn = CoreNamespace + "stream.origin.withdrawn"
	// TypeLinkStateChanged reports this site's link to its parent changing
	// state.
	TypeLinkStateChanged = CoreNamespace + "link.state.changed"

	// TypeLinkNodeApproved reports a person admitting one server under this
	// site.
	TypeLinkNodeApproved = CoreNamespace + "link.node.approved"
	// TypeLinkNodeRevoked reports a person taking away the admission of one
	// server under this site.
	TypeLinkNodeRevoked = CoreNamespace + "link.node.revoked"
	// TypeLinkNodeJoined reports a server this site had no record of, which is
	// now under it.
	TypeLinkNodeJoined = CoreNamespace + "link.node.joined"
	// TypeLinkNodeDenied reports the server above refusing this site's request
	// to join.
	TypeLinkNodeDenied = CoreNamespace + "link.node.denied"
	// TypeLinkNodeLost reports the connection to one server under this site
	// closing.
	TypeLinkNodeLost = CoreNamespace + "link.node.lost"
	// TypeLinkNodeReturned reports the connection to one server under this site
	// opening again.
	TypeLinkNodeReturned = CoreNamespace + "link.node.returned"

	// TypeDeviceRegistrationAnnounced reports a machine that has introduced
	// itself over a connector's wire and is waiting for somebody to decide.
	TypeDeviceRegistrationAnnounced = CoreNamespace + "device.registration.announced"
	// TypeDeviceRegistrationApproved reports a registration something let
	// through: an operator, or a connector configured to approve on arrival.
	TypeDeviceRegistrationApproved = CoreNamespace + "device.registration.approved"
	// TypeDeviceRegistrationRejected reports a registration an operator turned
	// away.
	TypeDeviceRegistrationRejected = CoreNamespace + "device.registration.rejected"
	// TypeDeviceRegistrationRefused reports a machine a connector turned away
	// at its own wire, because nothing let it in.
	TypeDeviceRegistrationRefused = CoreNamespace + "device.registration.refused"

	// TypeDeviceCommandRequested reports an instruction this host accepted and
	// is about to send to a device. It is the first of the three command
	// events.
	TypeDeviceCommandRequested = CoreNamespace + "device.command.requested"
	// TypeDeviceCommandSettled reports how one instruction ended, as far as
	// this host observed it.
	TypeDeviceCommandSettled = CoreNamespace + "device.command.settled"
	// TypeDeviceCommandAuthorized reports which principal authorized one
	// instruction. It is the audit frame, and reading it takes view_audit.
	TypeDeviceCommandAuthorized = CoreNamespace + "device.command.authorized"

	// TypeCameraSnapshotCaptured reports one still image a camera produced and
	// this host stored.
	TypeCameraSnapshotCaptured = CoreNamespace + "camera.snapshot.captured"
	// TypeDeviceTransferStarted reports a file this host has begun moving from
	// one of its storages onto a device. It is the first of the three
	// transfer events.
	TypeDeviceTransferStarted = CoreNamespace + "device.transfer.started"
	// TypeDeviceTransferProgressed reports how far a transfer has got. It is
	// telemetry: superseded by the next one and never stored.
	TypeDeviceTransferProgressed = CoreNamespace + "device.transfer.progressed"
	// TypeDeviceTransferSettled reports how one transfer ended.
	TypeDeviceTransferSettled = CoreNamespace + "device.transfer.settled"

	// TypeDeviceJobStarted reports a print this host has seen begin on a
	// device. It is the first of the two frames a print publishes.
	TypeDeviceJobStarted = CoreNamespace + "device.job.started"
	// TypeDeviceJobSettled reports how one print ended.
	TypeDeviceJobSettled = CoreNamespace + "device.job.settled"

	// TypeDashboardChanged reports a dashboard made, changed or deleted, so a
	// screen drawing it reads it again.
	TypeDashboardChanged = CoreNamespace + "dashboard.changed"
	// TypeServerUpgrade reports durable progress of the core upgrade
	// transaction. Its data is the complete journal at that transition.
	TypeServerUpgrade = CoreNamespace + "server.upgrade"
)

// TypeDoc is one core event type with its published text and its class.
type TypeDoc struct {
	// Type is the value of an envelope's type, which a consumer routes on.
	Type string `json:"type"`
	// Class is the queue, propagation and expiry policy for this type.
	Class Class `json:"class"`
	// Summary is one sentence saying what happened.
	Summary string `json:"summary"`
	// Description is the longer form, including what the data member contains.
	Description string `json:"description"`
	// Version is the vocabulary version this entry belongs to.
	Version string `json:"version"`
	// Deprecated marks a type that is going away, and is nil otherwise. A type
	// is removed only after a release published this marker for it.
	Deprecated *vocabulary.Deprecation `json:"deprecated,omitempty"`
}

// typeDocs is the descriptor table.
var typeDocs = []TypeDoc{
	{
		Type:    TypeDeviceArrived,
		Class:   ClassState,
		Summary: "A device joined the fleet and is now addressable.",
		Description: "Carries the device as the fleet knows it: its id, its model and the connector that " +
			"admitted it. It fires once per session, when the host takes the device and registers it. " +
			"A device that goes away and comes back without its session ending is carried by " +
			"com.prusa.connect.device.presence.changed rather than by a second arrival, so a bouncing " +
			"link does not read as a fleet of machines arriving. It is a state event rather than " +
			"telemetry, because a reader that missed it holds a fleet that is missing a device.",
	},
	{
		Type:    TypeDeviceDeparted,
		Class:   ClassState,
		Summary: "A device's connection ended.",
		Description: "Carries the device, the connector it arrived through and why the session ended. " +
			"The record is retained: departed means unreachable, not deleted. The device stays on " +
			"the listing with the last state it reported and the same reason, so a machine that was " +
			"here reads differently from an id that never answered. A reader keeps the device in its " +
			"list and marks it as gone, which is what stops a flapping connection emptying and " +
			"refilling a screen.",
	},
	{
		Type:    TypeDevicePresenceChanged,
		Class:   ClassState,
		Summary: "A device became unreachable, or reachable again, without its session ending.",
		Description: "Carries the device and whether it is reachable right now, so one frame states the " +
			"whole answer and a reader that missed the one before it is correct again on the next. It is " +
			"not a departure: the session is still open, so a command still queues for the machine and " +
			"the device is still in the fleet. A link that bounces is folded into at most one frame per " +
			"second per device, the last of which carries the state it settled on.",
	},
	{
		Type:    TypeDeviceLinkChanged,
		Class:   ClassState,
		Summary: "What this server is making of one device's frames changed.",
		Description: "Carries the device and the whole link reading: whether the connector counts " +
			"these at all, how many frames it had no reading of and how many telemetry patches it " +
			"dropped, both as a total for the session and as a rate over the window the reading was " +
			"taken across. It exists because a machine running firmware the server cannot read " +
			"connects, stays connected and publishes nothing, so it looks exactly like a machine that " +
			"is idle. One frame states the whole reading, and the reading is repeated while either " +
			"total is moving and once more after it settles, so a reader is never left showing a " +
			"storm that ended.",
	},
	{
		Type:    TypeDeviceIssuesChanged,
		Class:   ClassState,
		Summary: "What the host has found wrong with one device changed.",
		Description: "Carries the device and its whole issue list, worst first, in the shape the device " +
			"listing carries, and the dialog the device is waiting on a person for, when it waits for " +
			"one. A reader holding the device replaces both, and a reader that missed a frame is " +
			"correct again on the next. Changes that arrive together, such as a dialog's id and its " +
			"title, are folded into one frame that carries what they settled on, so a module that " +
			"sends a message per frame sends one. A departure is not repeated here: the device's " +
			"departure is its own event.",
	},
	{
		Type:    TypeDeviceStateChanged,
		Class:   ClassState,
		Summary: "The word a device reports for what it is doing changed.",
		Description: "Carries the device, the connector it arrived through, and the state before and after, " +
			"as the machine spells them, such as READY and PRINTING. It fires when the value at /state " +
			"differs from the one held. The first state of a device this server holds none for comes " +
			"with from empty, and a device repeating the state it is in fires nothing. The same value is " +
			"also published as com.prusa.connect.device.telemetry.sampled at /state; this frame exists " +
			"so a reader that waits for a device to come to rest, such as a print queue, need not read " +
			"every measurement of every device to find the few that change its state. It is a state " +
			"event rather than telemetry, because a reader that missed it would wait for a change that " +
			"already happened.",
	},
	{
		Type:    TypeDeviceTelemetrySampled,
		Class:   ClassTelemetry,
		Summary: "One measurement of a device's live state.",
		Description: "Conflated and at-most-once. A slow reader is given the newest sample rather than " +
			"every sample it missed, because a superseded temperature has no value and the queue it " +
			"would fill does.",
	},
	{
		Type:    TypeDeviceRecordChanged,
		Class:   ClassState,
		Summary: "The record this server stores about one device changed.",
		Description: "Carries the stored row: which connector holds the device, what the machine said it " +
			"is, its firmware, whether this server holds a socket to it, why the last session ended, " +
			"when it was last heard from and the tags an operator put on it. Beside them it names " +
			"which of those fields moved, so a " +
			"consumer acts on the change without diffing. It fires once per device per write to the " +
			"record store and only when one of the named fields moved, which is why a fleet reporting " +
			"telemetry at 5 Hz produces none of these: the state tree has its own frame per path and " +
			"last_seen rides along here without ever causing a frame. The tree is deliberately absent, " +
			"because every path in it is already published as " +
			"com.prusa.connect.device.telemetry.sampled and carrying it here would store every value " +
			"twice. One frame states the whole row, so a reader that missed the one before it is " +
			"correct again on the next.",
	},
	{
		Type:    TypeDeviceStoragesChanged,
		Class:   ClassState,
		Summary: "The storage devices one device reported changed.",
		Description: "Carries the device and the whole list, in the shape " +
			"/api/v1/devices/{device_id}/storages answers with, so one frame states the whole answer " +
			"and a reader that missed the one before it is correct again on the next. It fires when an " +
			"answer from the device differs from the one held, and on the first answer of each " +
			"connection, which is what turns reported true. Buddy firmware reports a drive put in or " +
			"pulled out on its own, and it answers when asked: on its connection, a root listing this " +
			"server holds no listing for, any root listing with refresh=true, and the storages route " +
			"with refresh=true.",
	},
	{
		Type:    TypeDeviceFilesChanged,
		Class:   ClassState,
		Summary: "A read of one device folder began or landed, or the folder changed.",
		Description: "Carries the device and the folder, as the storage-prefixed path " +
			"/api/v1/devices/{device_id}/files takes, and no rows. It fires when a read of the folder " +
			"begins, with reading true, and when that read lands, with reading false and failed saying " +
			"whether the device answered or refused, and when this server patched or forgot the " +
			"folder it holds. A reader that gets it reads the folder again with refresh=false, which " +
			"answers what this server holds and starts a read only when it holds nothing for the " +
			"folder. Its device origin makes it visible to whoever may see the device, as " +
			"com.prusa.connect.device.storages.changed is.",
	},
	{
		Type:    TypeDeviceCamerasChanged,
		Class:   ClassState,
		Summary: "The cameras listed for one device changed.",
		Description: "Carries the device and nothing else. It fires when what " +
			"/api/v1/cameras?device={device_id} lists for the device changes: a camera was bound to it " +
			"or taken off it, started or stopped serving, or changed its formats or its cadence. A " +
			"reader that gets it reads that listing again. The cameras are not in the frame, because a " +
			"camera carries grants of its own: a person may see a printer and not one of its cameras, " +
			"and the listing is what applies the camera's grants. Images, live views and uploads never " +
			"travel on the event stream, and neither does a value that changes with them, such as the " +
			"time of the last frame: only a change to which cameras the device has or to what one can " +
			"serve publishes this. A device publishes it at most once every 5 seconds. Changes inside " +
			"that time are folded into one frame at its end, sent only if the list still differs from " +
			"the one the last frame announced.",
	},
	{
		Type:    TypeCameraFrameReceived,
		Class:   ClassTelemetry,
		Summary: "A camera was handed a new frame.",
		Description: "Carries nothing but its origin, which is the camera's own device, so the camera's " +
			"grants decide who gets it. It fires when a camera that is sent its pictures, such as a " +
			"push camera, stores one, whether it came over HTTP or from a module. A reader that gets " +
			"it loads the camera's still again instead of asking on a clock. The frame itself never " +
			"travels on the stream. It is telemetry, so it is never stored, and a slow reader gets the " +
			"latest one.",
	},
	{
		Type:    TypeResourceStateChanged,
		Class:   ClassState,
		Summary: "A configured instance moved between lifecycle states.",
		Description: "Carries the instance's ref, its derived state and the conditions behind it. It is " +
			"how a badge in a user interface changes without polling the resource API.",
	},
	{
		Type:    TypeResourceUIChanged,
		Class:   ClassState,
		Summary: "A running module changed which entries of its own interface it hides.",
		Description: "Carries the instance's ref as resource, and as hidden the whole list the module " +
			"hides now, each entry a slot key and an identity such as pages/customers. It is how the " +
			"web app takes a page out of the sidebar, or puts it back, without a reload: it reads " +
			"GET /api/v1/resources again, where the list is ui.hidden. The operator's own " +
			"hidden_contributions are not in it.",
	},
	{
		Type:    TypeLinkStateChanged,
		Class:   ClassState,
		Summary: "This site's link to its parent site changed state.",
		Description: "Carries the parent's address, how the parent's authorization applies on this " +
			"site, the link's state and the reason for it, and the fingerprint of the key this site " +
			"proves itself with. One frame states the whole reading, so a reader that missed the one " +
			"before it is correct again on the next. A parent that is lost is reported here rather " +
			"than in a log, because a person watching a screen is the one who has to know.",
	},
	{
		Type:    TypeLinkNodeApproved,
		Class:   ClassState,
		Summary: "A person admitted one server under this site.",
		Description: "Carries the link instance, the id of the server, and the person who decided, as " +
			"auth_type and auth_id. The host publishes it on the route the person called, after the " +
			"link confirmed the decision, so the record names the person and never the link. A module " +
			"cannot publish it: every type a module emits starts with the module's own namespace. It " +
			"is a state event, so a webhook and a history store receive it under the grant on the " +
			"link instance and take no further one.",
	},
	{
		Type:    TypeLinkNodeRevoked,
		Class:   ClassState,
		Summary: "A person took away the admission of one server under this site.",
		Description: "Carries the link instance, the id of the server, and the person who decided, as " +
			"auth_type and auth_id. The server stays under this site with its admission marked " +
			"revoked, so an operator still sees the server that was refused. It is published the same " +
			"way the approval is: on the route the person called, after the link confirmed it.",
	},
	{
		Type:    TypeLinkNodeJoined,
		Class:   ClassState,
		Summary: "A server this site had no record of is now under it.",
		Description: "Carries the link instance, the id the server joined under, where it sits in the " +
			"tree, whether this site lets it act here yet, and its public identity key. The host " +
			"derives it by comparing the reading a link reports with the reading before it, so a link " +
			"needs no new call and cannot report a server twice. A site that keeps every new server " +
			"waiting reports the admission as pending here, and the person who then approves it is in " +
			"com.prusa.connect.link.node.approved.",
	},
	{
		Type:    TypeLinkNodeDenied,
		Class:   ClassState,
		Summary: "The server above refused this site's request to join.",
		Description: "Carries the link instance, the address of the server above, the protocol's own " +
			"number for the refusal, the link's registered code for it and the sentence the server " +
			"above wrote. The two identifiers mean different things: the number is the other end's " +
			"and the code is the link's, which is what a page keys its own sentence by. It is a state " +
			"event rather than an audit one, because nobody on this site decided it.",
	},
	{
		Type:    TypeLinkNodeLost,
		Class:   ClassState,
		Summary: "The connection to one server under this site closed.",
		Description: "Carries the link instance, the id of the server, that the connection is not open, " +
			"and when something last arrived from it. The server stays under this site, so a reader " +
			"marks it as gone rather than dropping it. The host derives it by comparing one reading " +
			"with the reading before it, so a link that reports the same reading twice publishes " +
			"nothing.",
	},
	{
		Type:    TypeLinkNodeReturned,
		Class:   ClassState,
		Summary: "The connection to one server under this site opened again.",
		Description: "Carries the link instance, the id of the server, that the connection is open, and " +
			"when something last arrived from it. It is the answer to " +
			"com.prusa.connect.link.node.lost, so a screen that marked a server as gone clears the " +
			"mark on this frame rather than on the next whole reading.",
	},
	{
		Type:    TypeStreamContinuityLost,
		Class:   ClassState,
		Summary: "This reader cannot be resumed from its cursor without a gap.",
		Description: "The events between the reader's cursor and the stream's oldest retained position " +
			"are gone. A reader that receives it refetches the state it cares about rather than " +
			"assuming the gap held nothing.",
	},
	{
		Type:    TypeStreamOriginWithdrawn,
		Class:   ClassState,
		Summary: "An origin this reader followed is no longer one it may see.",
		Description: "Carries the origin, as kind:id. It is sent for an origin the reader held, or named in " +
			"the cursor it resumed from, and never for one it could not see. Whatever the stream had " +
			"queued for that origin is dropped before it is sent. It does not say whether the origin " +
			"still exists: a removed extension, a removed printer and a grant taken away read the same. " +
			"A reader that named its origins explicitly is not sent it: its stream is closed instead.",
	},
	{
		Type:    TypeDeviceRegistrationAnnounced,
		Class:   ClassState,
		Summary: "A machine introduced itself over a connector's wire and is waiting for a decision.",
		Description: "Carries the code the machine is displaying, the connector holding it, the " +
			"fingerprint it keeps across reconnections, and what it said it is. The origin is the " +
			"connector instance and not a device, because no device exists yet: an id is minted when " +
			"the registration is approved. A page adding a printer subscribes to this instead of " +
			"asking /api/v1/registrations every second. It fires once per code, when the host first " +
			"writes the registration down, so a machine that retries its handshake produces one " +
			"frame and not one per retry. A code a connector approves the moment it arrives is not " +
			"announced, because nobody has to decide it: com.prusa.connect.device.registration.approved " +
			"is its only frame. It is a state event rather than telemetry, because a " +
			"reader that missed it is waiting for a code that has already arrived.",
	},
	{
		Type:    TypeDeviceRegistrationApproved,
		Class:   ClassState,
		Summary: "A registration was let through, and the machine behind it now has a device id.",
		Description: "Carries the code, the connector, the fingerprint and the device id the approval " +
			"bound the machine to, so a page that approved a code opens the device page from this " +
			"frame alone. The device id is empty for a registration carrying no fingerprint, because " +
			"there is nothing to bind an id to and the machine is given one at its first handshake. " +
			"It is published for an operator's approval and for a connector configured to approve on " +
			"arrival alike. Approving does not open a connection: the machine appears on " +
			"/api/v1/devices once it has dialled, which is com.prusa.connect.device.arrived.",
	},
	{
		Type:    TypeDeviceRegistrationRejected,
		Class:   ClassState,
		Summary: "A registration was turned away.",
		Description: "Carries the code, the connector and the fingerprint of a registration an " +
			"operator refused. Every credential it held is forgotten with it, so a machine that was " +
			"already approved stops being accepted on that connector's wire. It is not a permanent " +
			"refusal: the machine registers again at its next boot and is given a new code, which " +
			"announces itself here as any other code does.",
	},
	{
		Type:    TypeDeviceRegistrationRefused,
		Class:   ClassState,
		Summary: "A machine called a connector's wire and was turned away.",
		Description: "Carries the connector, why the machine was refused, whether that connector asks a " +
			"person to approve each machine or approves them on arrival, and the model and firmware " +
			"the machine reported. The reason is unknown_credentials for credentials this server did " +
			"not issue, and not_admitted for a machine the host would not take, such as one somebody " +
			"retired. A refused machine usually calls again every few seconds, so the connector " +
			"publishes at most one frame a minute for each machine, and a bounded number a minute in " +
			"all. The machine's identity is never " +
			"in the payload, only its SHA-256 digest, and neither is its network address.",
	},
	{
		Type:    TypeDeviceCommandRequested,
		Class:   ClassCommand,
		Summary: "An instruction was accepted for a device and is about to be sent.",
		Description: "Carries the host-minted command_id, the verb, the caller's idempotency key, the " +
			"capability epoch and digest the command was validated against, and the arguments as the " +
			"caller sent them. It is the only one of the three command frames that carries a TTL, " +
			"because it is the instruction and an instruction has a deadline. A consumer that acts on " +
			"it checks the envelope's expiry first; a consumer that stores it does not. Arguments of a " +
			"verb marked dangerous are published as their SHA-256 digest and never as text, and so are " +
			"arguments that would break the 4 KiB bound on this payload. It carries no principal: who " +
			"authorized the command is in com.prusa.connect.device.command.authorized alone.",
	},
	{
		Type:    TypeDeviceCommandSettled,
		Class:   ClassState,
		Summary: "An instruction ended, and this is what the host observed.",
		Description: "Carries the command_id, the verb, how the command ended and how long it took. The " +
			"status is one of succeeded, failed, refused, timed_out or abandoned, and a command whose " +
			"caller went away while it may already have been on the wire is marked with the outcome " +
			"unknown rather than guessed. It has no TTL, because the record of what happened never " +
			"expires. It repeats none of the arguments: a consumer folds this frame together with the " +
			"requested one by the causation id they share.",
	},
	{
		Type:    TypeDeviceCommandAuthorized,
		Class:   ClassAudit,
		Summary: "A principal authorized one instruction for a device.",
		Description: "Carries the command_id, the verb and the authorizing principal as auth_type and " +
			"auth_id, the same pair the envelope's audit attributes carry. The pair is in the payload " +
			"as well as on the envelope because a store keeps the payload and not the envelope, and an " +
			"audit record with no actor is not an audit record. Seeing it takes view_audit on the " +
			"device, on the live stream and on the history read alike: viewing a printer is not being " +
			"told which operator commanded it.",
	},
	{
		Type:    TypeCameraSnapshotCaptured,
		Class:   ClassState,
		Summary: "A camera produced a still image and the host stored it.",
		Description: "Carries the camera, the printer it watches, the storage instance and the path the " +
			"image was written to, its size in bytes and the moment the device produced the frame. It " +
			"carries no image: a frame is megabytes and an event is a record of something happening, " +
			"so what is here is where to fetch the picture from. It is published once per stored " +
			"snapshot by the periodic capture an operator switches on per camera, and never by a " +
			"snapshot somebody fetched over the API, because a read is not a thing that happened to " +
			"the machine. It is a state event rather than telemetry, because a reader that missed one " +
			"has a gap in the pictures of a print rather than a stale reading.",
	},
	{
		Type:    TypeDeviceTransferStarted,
		Class:   ClassState,
		Summary: "This host began moving a file from one of its storages onto a device.",
		Description: "Carries the transfer_id, the storage and path the bytes come from, where the file " +
			"lands on the device, and the file's length as the host measured it before the first byte " +
			"left. The bytes move through this host: the device is never handed a URL to fetch, so " +
			"this frame is the record that the file left a storage this deployment holds. It is a " +
			"state event, because a reader that missed it holds a transfer it can never account for.",
	},
	{
		Type:    TypeDeviceTransferProgressed,
		Class:   ClassTelemetry,
		Summary: "A transfer moved.",
		Description: "Carries the transfer_id, the length, how many bytes this host has put on the wire " +
			"and, for a device that reports one, how many the device says it has taken. It is " +
			"telemetry: it is conflated on the way to a subscriber and it is not stored, because a " +
			"record of every range of every file would grow with bytes moved rather than with things " +
			"that happened. What a later reader gets is the started frame, the settled frame and the " +
			"row folded from the two.",
	},
	{
		Type:    TypeDeviceTransferSettled,
		Class:   ClassState,
		Summary: "A transfer ended.",
		Description: "Carries the transfer_id, how it ended, how many of how many bytes had left, why, " +
			"and how long it ran. The state is one of done, failed, cancelled or interrupted. " +
			"Interrupted is written on the next start for a transfer that was still running when this " +
			"server stopped: it is not resumed, because the device has forgotten the transfer and the " +
			"bytes it holds are a prefix nobody can name. A consumer folds this frame together with " +
			"the started one by the transfer_id they share.",
	},
	{
		Type:    TypeDeviceJobStarted,
		Class:   ClassState,
		Summary: "A print began on a device.",
		Description: "Carries the job_id, the file on the machine and the name the machine shows for " +
			"it, the instruction that started the print, and when this host first saw it. The " +
			"command_id is absent for a print somebody started at the machine's own screen, because " +
			"no instruction was sent. It names no person: who authorized the print is in " +
			"com.prusa.connect.device.command.authorized, which takes view_audit, and the two are " +
			"joined by the command_id. It fires once per print, on the first write of that print's " +
			"row, so a host that restarts mid-print publishes nothing a second time. Progress is " +
			"telemetry at /job/progress and is not here.",
	},
	{
		Type:    TypeDeviceJobSettled,
		Class:   ClassState,
		Summary: "A print ended.",
		Description: "Carries the job_id, the file, when the print ran, how it ended, the machine's " +
			"own last word for it, the last progress reported, how long the machine says it printed " +
			"and how much filament it says the print used. The outcome is one of finished, failed, " +
			"cancelled and unknown, and it is the driver's reading of its machine's own word: a print " +
			"that stopped without one is unknown, which is what a power cut looks like. The time " +
			"printed and the filament used are absent for a machine that reports neither, and absent " +
			"means unknown and never zero. It fires once per print, on the first reading that gives " +
			"the print an end, so a machine that reports two terminal readings publishes one frame. A " +
			"machine that loses its connection mid-print settles nothing: the print stays open and " +
			"this frame follows when the machine comes back and says what happened.",
	},
	{
		Type:    TypeDashboardChanged,
		Class:   ClassState,
		Summary: "A dashboard was made, changed or deleted.",
		Description: "Carries the dashboard's id and the action: created, changed or deleted. The " +
			"origin is the group of people who see it, dashboard:everyone, dashboard:operators or " +
			"dashboard:admins, and a change that narrows who sees a dashboard is published to the group " +
			"that saw it before. The frame carries no part of the dashboard: a screen drawing it reads " +
			"it again, and one no longer allowed to see it is answered DASHBOARD_NOT_FOUND. Choosing " +
			"the default publishes a change for the dashboard that stopped being it and for the one " +
			"that became it.",
	},
	{
		Type:    TypeServerUpgrade,
		Class:   ClassState,
		Summary: "The core upgrade transaction advanced.",
		Description: "Carries the complete durable upgrade journal after a stage, verification, check, " +
			"schedule or cancellation request. Its server origin is visible only to organization " +
			"administrators. A reconnecting client reads the upgrade endpoint as its source of truth.",
	},
}

// TypeDocs returns every core event type, in declaration order.
func TypeDocs() []TypeDoc {
	out := make([]TypeDoc, len(typeDocs))
	for i, doc := range typeDocs {
		out[i] = TypeDoc{
			Type:        doc.Type,
			Class:       doc.Class,
			Summary:     doc.Summary,
			Description: doc.Description,
			Version:     VocabularyVersion,
		}
	}

	return out
}

// Types returns the core event type names, in declaration order.
func Types() []string {
	out := make([]string, 0, len(typeDocs))
	for _, doc := range typeDocs {
		out = append(out, doc.Type)
	}

	return out
}

// LookupType returns the descriptor for a core event type, and false for a
// name that is not in the vocabulary.
func LookupType(name string) (TypeDoc, bool) {
	for _, doc := range typeDocs {
		if doc.Type == name {
			return TypeDoc{
				Type:        doc.Type,
				Class:       doc.Class,
				Summary:     doc.Summary,
				Description: doc.Description,
				Version:     VocabularyVersion,
			}, true
		}
	}

	return TypeDoc{}, false
}

// ValidateTypeSyntax reports whether name is a well-formed event type: dotted
// lowercase segments, at most 128 bytes.
//
// It is a security check. A type is written verbatim into an SSE `event:`
// field and a messaging subject, so a newline in it would inject a frame.
func ValidateTypeSyntax(name string) error {
	if name == "" {
		return fmt.Errorf("%w: type is empty", ErrInvalidEnvelope)
	}

	if len(name) > maxTypeLen {
		return fmt.Errorf("%w: type %q is %d bytes, the limit is %d",
			ErrInvalidEnvelope, echo(name), len(name), maxTypeLen)
	}

	// Counted and then walked without a slice, because this runs once per
	// published event.
	if strings.Count(name, typeSeparator)+1 < minTypeSegments {
		return fmt.Errorf("%w: type %q is not reverse-DNS", ErrInvalidEnvelope, echo(name))
	}

	for segment := range strings.SplitSeq(name, typeSeparator) {
		if !validTypeSegment(segment) {
			return fmt.Errorf(
				"%w: type %q carries %q, and a segment is lowercase letters, digits, '-' and '_'",
				ErrInvalidEnvelope, echo(name), echo(segment))
		}
	}

	return nil
}

// ValidateType is ValidateTypeSyntax plus membership. A name under
// CoreNamespace that is not in the vocabulary is refused. A name outside the
// namespace is checked for syntax only.
func ValidateType(name string) error {
	if err := ValidateTypeSyntax(name); err != nil {
		return err
	}

	if !strings.HasPrefix(name, CoreNamespace) {
		return nil
	}

	// The table is scanned directly, because Types allocates and this runs
	// once per published event.
	if !declaredType(name) {
		return fmt.Errorf(
			"%w: type %q is in the %s namespace and is not in the event-type vocabulary. Declare it in "+
				"sdk/event, or emit it under a namespace of your own",
			ErrInvalidEnvelope, echo(name), CoreNamespace)
	}

	return nil
}

// declaredType reports whether the vocabulary has this name.
func declaredType(name string) bool {
	for _, doc := range typeDocs {
		if doc.Type == name {
			return true
		}
	}

	return false
}

// validTypeSegment reports whether one dot-separated segment is well formed. It
// refuses every control character.
func validTypeSegment(segment string) bool {
	if segment == "" {
		return false
	}

	for i := range len(segment) {
		c := segment[i]

		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '-' || c == '_') && i > 0 && i < len(segment)-1:
		default:
			return false
		}
	}

	return true
}
