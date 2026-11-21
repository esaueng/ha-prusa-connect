package event

// StateChanged is the data member of com.prusa.connect.device.state.changed:
// the word a device reports for what it is doing, such as IDLE, READY or
// PRINTING, before and after it changed.
type StateChanged struct {
	// DeviceID is the device whose state changed.
	DeviceID string `json:"device_id"`
	// Driver is the connector the device arrived through.
	Driver string `json:"driver"`
	// From is the state the device reported before, as the machine spells it.
	// It is empty when this server held no state for the device.
	From string `json:"from"`
	// To is the state the device reports now, as the machine spells it.
	To string `json:"to"`
}
