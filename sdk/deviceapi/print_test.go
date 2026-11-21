package deviceapi_test

import (
	"errors"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// A body that names half a source is refused on the client's side, before a
// round trip: a source inside no storage and a storage with no path in it both
// name no file, and neither can be answered by guessing what the caller meant.
func TestAPrintNamingHalfASourceIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]deviceapi.PrintParams{
		"a storage with no path inside it": {ID: "p1", Storage: "bench", Path: "usb/part.bgcode"},
		"a path inside no storage":         {ID: "p1", Source: "part.bgcode", Path: "usb/part.bgcode"},
		"no file on the printer":           {ID: "p1", Storage: "bench", Source: "part.bgcode"},
		"keep_both with no storage":        {ID: "p1", Path: "usb/part.bgcode", KeepBoth: true},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := params.Validate(); !errors.Is(err, deviceapi.ErrInvalidQuery) {
				t.Fatalf("the params were held to be %v, want %v", err, deviceapi.ErrInvalidQuery)
			}
		})
	}
}

// A request naming a storage and a file inside it, and one naming a file the
// printer already holds, are both whole requests.
func TestAWholePrintRequestPasses(t *testing.T) {
	t.Parallel()

	cases := map[string]deviceapi.PrintParams{
		"a file from a storage": {
			ID: "p1", Storage: "bench", Source: "part.bgcode", Path: "usb/part.bgcode",
		},
		"a file the printer already holds": {ID: "p1", Path: "usb/part.bgcode"},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := params.Validate(); err != nil {
				t.Fatalf("a whole request was refused: %v", err)
			}
		})
	}
}

// Every field carries a bound, and a value over it is refused rather than cut.
func TestAPrintOverTheBoundsIsRefused(t *testing.T) {
	t.Parallel()

	long := func(n int) string {
		out := make([]byte, n)
		for index := range out {
			out[index] = 'a'
		}

		return string(out)
	}

	cases := map[string]deviceapi.PrintParams{
		"the path":      {ID: "p1", Path: long(deviceapi.MaxPathBytes + 1)},
		"the storage":   {ID: "p1", Path: "usb/p.bgcode", Storage: long(deviceapi.MaxStorageBytes + 1), Source: "p"},
		"the source":    {ID: "p1", Path: "usb/p.bgcode", Storage: "bench", Source: long(deviceapi.MaxSourceBytes + 1)},
		"no device":     {Path: "usb/p.bgcode"},
		"the device id": {ID: long(deviceapi.MaxIDBytes + 1), Path: "usb/p.bgcode"},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := params.Validate(); err == nil {
				t.Fatal("a request outside the published bounds was accepted")
			}
		})
	}
}

// The host reads the params as the module wrote them, and the two ids come
// back on the answer.
func TestTheHostReadsAPrintAsItWasWritten(t *testing.T) {
	t.Parallel()

	fleet := &fakeFleet{started: deviceapi.Started{TransferID: "01TRANSFER"}}

	params := deviceapi.PrintParams{
		Principal: "user:alpha", ID: "p1",
		Storage: "bench", Source: "models/part.bgcode", Path: "usb/part.bgcode",
	}

	answered, err := call(t, deviceapi.Handlers(fleet), deviceapi.MethodPrint, params)
	if err != nil {
		t.Fatalf("print: %v", err)
	}

	if fleet.sawPrint != params {
		t.Fatalf("the host was asked for %+v, want %+v", fleet.sawPrint, params)
	}

	started, ok := answered.(deviceapi.Started)
	if !ok || started.TransferID != "01TRANSFER" {
		t.Fatalf("the answer is %+v, want the transfer the host opened", answered)
	}
}

// A storage the caller may not read and a file that is not there are one
// refusal, and it reaches the module as this contract's own sentinel.
func TestAPrintRefusalCrossesAsItsSentinel(t *testing.T) {
	t.Parallel()

	cases := map[error]int{
		deviceapi.ErrNoFile:       deviceapi.CodeNoFile,
		deviceapi.ErrDenied:       deviceapi.CodeDenied,
		deviceapi.ErrInvalidQuery: deviceapi.CodeInvalidQuery,
		deviceapi.ErrNameTaken:    deviceapi.CodeNameTaken,
		deviceapi.ErrPrintRefused: deviceapi.CodePrintRefused,
		deviceapi.ErrPrintFailed:  deviceapi.CodePrintFailed,
	}

	for sentinel, code := range cases {
		t.Run(sentinel.Error(), func(t *testing.T) {
			t.Parallel()

			fleet := &fakeFleet{err: sentinel}

			_, err := call(t, deviceapi.Handlers(fleet), deviceapi.MethodPrint,
				deviceapi.PrintParams{ID: "p1", Path: "usb/part.bgcode"})

			var coded *wirerpc.Error
			if !errors.As(err, &coded) || coded.Code != code {
				t.Fatalf("the refusal crossed as %v, want code %d", err, code)
			}
		})
	}
}
