package fault

// The core.printer codes: reaching a printer at all, and whether this server is
// the one allowed to command it.
var (
	// PrinterNotFound reports an id no registered printer answers to.
	PrinterNotFound = Register(DomainPrinter(), "NOT_FOUND", statusNotFound, Document(
		"No printer is registered with that id",
		"Read the id from the printer list. An id that was removed is not reissued, so a stored one can go stale.",
		RetryNever,
		ShowDetail,
		"device_id",
	))

	// PrinterReadOnlyShadow refuses a command aimed at a federated peer's
	// printer, which this server mirrors for reading only.
	PrinterReadOnlyShadow = Register(DomainPrinter(), "READ_ONLY_SHADOW", statusConflict, Document(
		"The printer is a read-only mirror of one owned by another server",
		"Send the command to the server that owns the printer. State and events for it stay readable here.",
		RetryNever,
		ShowDetail,
		"device_id",
	))

	// PrinterSessionEnded refuses an operation that has to reach the machine,
	// for a printer whose session has ended. The printer is still readable: its
	// last state and the reason it went are on the listing.
	PrinterSessionEnded = Register(DomainPrinter(), "SESSION_ENDED", statusConflict, Document(
		"The printer's session has ended",
		"Wait for the printer to connect again. Its last state stays readable while it is away.",
		RetryAllowed,
		ShowDetail,
		"device_id",
		// command_id is on this code because a command refused here is still
		// recorded: the id names the row the refusal wrote, and fault.With
		// drops a param the code does not declare, so a code reachable from a
		// refusal path has to name it or the promise to carry it is empty.
		"command_id",
	))

	// PrinterTransferNotFound reports a transfer id that names nothing this
	// server is running.
	//
	// A transfer that has already ended and one that never existed are the same
	// answer on purpose. A caller who could tell them apart could learn which
	// files a printer it may not read has been sent, by asking for ids until one
	// answered differently.
	PrinterTransferNotFound = Register(DomainPrinter(), "TRANSFER_NOT_FOUND", statusNotFound, Document(
		"No transfer of that id is running",
		"Read the transfer id from the printer's transfer list. A transfer that has already ended "+
			"cannot be stopped, and reads the same as one that never existed.",
		RetryNever,
		ShowDetail,
		"transfer_id",
	))

	// PrinterFileNameTaken refuses to send a file to a name the printer's
	// storage already holds. The printer refuses such a transfer itself, and
	// replacing its file deletes something the person may want, so the person
	// is asked first and the request carries replace when they agree.
	// read_only is true for a file the printer refuses to delete now, such as
	// one open on its screen or still arriving, which is not offered to replace.
	PrinterFileNameTaken = Register(DomainPrinter(), "FILE_NAME_TAKEN", statusConflict, Document(
		"A file of that name is already on the printer",
		"Ask the person whether to replace the file on the printer. If they agree, send the same "+
			"request again with replace set to true, which deletes the printer's file first. Otherwise "+
			"send the file under another name.",
		RetryNever,
		ShowDetail,
		"device_id",
		"path",
		"read_only",
	))

	// PrinterJobNotFound refuses a print id the caller cannot read.
	//
	// A print on a device the caller reaches nothing on and an id nobody ever
	// minted are the same answer, for the reason a transfer's are: a caller who
	// could tell them apart could learn which prints exist by asking for ids.
	PrinterJobNotFound = Register(DomainPrinter(), "JOB_NOT_FOUND", statusNotFound, Document(
		"No print of that id is held",
		"Read the print id from a printer's jobs or its print history. A print on a printer you "+
			"cannot see reads the same as one that never existed.",
		RetryNever,
		ShowDetail,
		"job_id",
	))

	// PrinterVerbUnknown refuses a verb this build's capability vocabulary does
	// not name.
	//
	// It is separate from core.printer/CAPABILITY_NOT_SUPPORTED, which is one
	// printer's answer about a verb that exists. This one is the server's answer
	// about a verb that does not, and it names no printer: it is decided before
	// any printer is resolved, so a caller holding nothing learns nothing from
	// it.
	//
	// suggested_verb keeps that true. It is derived from the caller's own
	// spelling and from a vocabulary published to a caller holding no
	// credential, so it is the same answer for every caller and for every
	// printer id, including one no printer answers to.
	//
	// kind is the second refusal this code carries, and it is a different one:
	// a verb some device kind in this build declares, sent to a device of
	// another kind. That answer does name a fact about a device, so it is given
	// only to a caller who may already read that device; a caller holding
	// nothing gets the ordinary not-found instead.
	PrinterVerbUnknown = Register(DomainPrinter(), "VERB_UNKNOWN", statusBadRequest, Document(
		"This build has no such capability verb",
		"Send a verb from the capability vocabulary at /api/v1/vocabularies/capabilities, and one "+
			"the device's own kind declares: the verb table has a row per kind, so a verb another "+
			"kind carries does not exist for this one, and kind says which kind the device is. "+
			"A spelling that differs from a verb only in case or in the missing core.command. prefix "+
			"is answered with suggested_verb, which is this API's name for it. The shipping server's "+
			"SET_NOZZLE_TEMPERATURE is core.command.set_nozzle_temperature here.",
		RetryNever,
		ShowDetail,
		"verb",
		"suggested_verb",
		"kind",
	))

	// PrinterPresetModelMismatch refuses a stored gcode preset on a printer
	// whose model the preset does not name.
	//
	// It is separate from core.validation/REQUEST_INVALID, which reports a
	// request that failed the operation's schema. This request is well formed:
	// the preset exists, the printer exists, and gcode written for one model is
	// refused on another. The two models are parameters so a client can offer
	// the presets that do run on the printer it is showing, rather than reading
	// a sentence.
	PrinterPresetModelMismatch = Register(
		DomainPrinter(), "PRESET_MODEL_MISMATCH", statusUnprocessable, Document(
			"The preset names printer models and this printer is not one of them",
			"Run a preset that names this printer's model, or one that names no model at all. Gcode "+
				"that is safe on one model is not safe on another, so nothing was sent.",
			RetryNever,
			ShowDetail,
			"printer_model", "preset_models",
		))

	// PrinterVerbNotAllowedInState refuses a verb the device has and the state
	// it is in does not accept. It is this server's answer, decided above the
	// driver, and ADR-20260831-c7b rules 30 and 31 keep it apart from the three
	// refusals it is easiest to confuse it with: core.printer/VERB_UNKNOWN for a
	// verb this build does not name, core.printer/NOT_FOUND for a caller who may
	// not act on the device, and core.printer/COMMAND_REJECTED for the machine's
	// own refusal in the machine's own words.
	//
	// state is the state the device reported, in the names its adapter declared.
	// A client renders the refusal from the payload and reads nothing back.
	PrinterVerbNotAllowedInState = Register(
		DomainPrinter(), "VERB_NOT_ALLOWED_IN_STATE", statusConflict, Document(
			"The device's state does not allow that verb",
			"Send the verb again once the device reports a state that allows it. The state named here is "+
				"the one the refusal was decided on, so a device that has changed state since answers "+
				"differently.",
			RetryAllowed,
			ShowDetail,
			"device_id", "verb", "state",
		))

	// PrinterCommandRejected reports a command the printer itself refused, as
	// distinct from one this server declined to send.
	PrinterCommandRejected = Register(DomainPrinter(), "COMMAND_REJECTED", statusConflict, Document(
		"The printer refused the command",
		"Check what the printer is doing and send it again. A printer refuses a command its current state does not allow.",
		RetryAllowed,
		ShowDetail,
		"device_id",
	))
)
