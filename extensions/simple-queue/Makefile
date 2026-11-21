# The gates of the print queue module. extensions/Makefile runs `check` here,
# and so does `make -C extensions check EXT=simple-queue`.

include ../extension.mk

.PHONY: check e2e test

# Lint the Go half, then build both halves, install the release into a demo
# server and print a queue on a simulated printer. See README.md.
check: lint e2e

e2e:
	bash scripts/check.sh

# The Go half alone, in its own folder, for a quick run while editing it.
test:
	go build ./...
	go vet ./...
	go test -count=1 ./...
