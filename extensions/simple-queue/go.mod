// A module that keeps one print queue for each printer of this server.
//
// It carries its own go.mod so that it builds the way a module from somebody
// else's repository builds: against the published sdk alone, outside the
// core's Go module. `go build ./...` in the core walks past this directory.
//
// Until the sdk is tagged, the replace below binds it to the checkout beside
// this directory, which is what a development build does. A released module
// requires a published sdk version and carries no replace.
module github.com/prusa3d/simple-queue

go 1.26.11

require (
	github.com/prusa3d/prusa-connect/sdk v0.0.0-00010101000000-000000000000
	github.com/lib/pq v1.12.3
	modernc.org/sqlite v1.60.1
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.48.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/prusa3d/prusa-connect/sdk => ../../sdk
