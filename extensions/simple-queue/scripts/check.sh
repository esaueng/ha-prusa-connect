#!/bin/sh
# The print queue module, built, installed and driven end to end.
#
#   extensions/simple-queue  a module with a Go half and a browser half, in
#                            its own Go module, that keeps a queue of print
#                            files for each printer
#
# What it does, in order:
#
#   1. builds, vets and tests the Go half inside its own folder, which is what
#      somebody who cloned the module alone does
#   2. holds the typed client to the module's OpenAPI fragment
#   3. lints and type-checks the browser half, runs its tests in Chromium,
#      checks every component has its stories, and builds the Storybook
#   4. lays the module out as a release and installs it with
#      `prusa-connect module add`
#   5. starts the demo with one simulated printer that prints only what it is
#      sent, puts two G-code files on its queue, and checks that the first
#      starts by itself once the bed is marked clear, that the second waits
#      for a person after it, and that both end as finished
#
# The whole run is one function, called on the last line, so the shell parses
# this file to its end before executing any of it.
main() {

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$ROOT" || exit 1

MODULE_DIR=extensions/simple-queue
CLIENT_DIR=$MODULE_DIR/client
UI_DIR=$MODULE_DIR/ui

# The component library the bundle is built against, and the client SDK the
# typed client links to. A fresh clone has no dist under either.
LIBRARY_DIR=packages/svelte
CLIENT_SDK_DIR=packages/typescript

# Everything this run writes goes under bin/, which git ignores, and that is
# where a build artefact belongs. One run at a time owns it: a second run that
# starts while the first is going stops at the lock below.
WORK_ROOT=bin/simple-queue
LOCK=$WORK_ROOT.lock
RELEASE=$WORK_ROOT/release
MODULES_DIR=$WORK_ROOT/modules
DEMO_DIR=$WORK_ROOT/demo
OUT_CONFIG=${QUEUE_CONFIG:-$WORK_ROOT/config.yaml}

# The module path the module is installed from, and the namespace the host
# derives from it. The author never writes the namespace: it is the reversed
# module path, and every address of the module is built from it.
QUEUE_MODULE=github.com/prusa3d/simple-queue
QUEUE_NAMESPACE=com.github.aux.prusa3d.simple-queue
MOUNT=/api/v1/modules/$QUEUE_NAMESPACE

# The demo binds the first free port from QUEUE_PORT up, 18348 by default. It
# is not 8080, so a run does not collide with a server somebody already has up.
# DEMO_ADDR and BASE_URL are set once a port is chosen, before the demo starts.
FIRST_PORT=${QUEUE_PORT:-18348}


CHECKSUMS_FILE=checksums.txt

# CI=true keeps pnpm from asking anything, and keeps its output readable.
CI=true
export CI

for tool in go pnpm sha256sum curl bash; do
	if ! command -v "$tool" > /dev/null 2>&1; then
		echo "FAIL  $tool is not on PATH, and this lane needs it."
		exit 1
	fi
done

WORK=$(mktemp -d)
DEMO_PID=

# cleanup runs on every way out: success, a failed step, Ctrl-C and a kill. It
# stops the demo this run started, removes the scratch files and gives up the
# lock. A signal exits with 128 plus its number, so a caller sees the run as
# interrupted and not as passed.
cleanup() {
	if [ -n "$DEMO_PID" ]; then
		kill "$DEMO_PID" 2> /dev/null
		wait "$DEMO_PID" 2> /dev/null
	fi
	rm -rf "$WORK"
	if [ "$(cat "$LOCK/pid" 2> /dev/null)" = "$$" ]; then
		rm -rf "$LOCK"
	fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# take_lock makes $LOCK, which only one process can do. A lock whose run has
# ended is stale: that run was killed before its cleanup. Its demo may still be
# up, so it is stopped here, before this run deletes the files under it.
take_lock() {
	mkdir -p "$(dirname "$LOCK")"
	if mkdir "$LOCK" 2> /dev/null; then
		echo "$$" > "$LOCK/pid"
		return 0
	fi

	owner=$(cat "$LOCK/pid" 2> /dev/null)
	if [ -n "$owner" ] && kill -0 "$owner" 2> /dev/null; then
		echo "FAIL  another run of this check (pid $owner) is using $WORK_ROOT."
		echo "      Wait for it to end, or stop it."
		exit 1
	fi

	stale=$(cat "$LOCK/demo.pid" 2> /dev/null)
	if [ -n "$stale" ] && ps -p "$stale" -o command= 2> /dev/null | grep -q demo-server; then
		echo "    stopping the demo (pid $stale) that an interrupted run left running"
		kill "$stale" 2> /dev/null
		sleep 1
	fi

	rm -rf "$LOCK"
	if ! mkdir "$LOCK" 2> /dev/null; then
		echo "FAIL  another run of this check took $LOCK first."
		exit 1
	fi
	echo "$$" > "$LOCK/pid"
}

take_lock

STEPS=0
step() {
	WHAT=$1
	shift
	STEPS=$((STEPS + 1))

	printf '%2d. %s...\n' "$STEPS" "$WHAT"

	if "$@" > "$WORK/step.log" 2>&1; then
		return 0
	fi

	echo "FAIL  $WHAT"
	cat "$WORK/step.log"
	exit 1
}

rm -rf "$WORK_ROOT"
mkdir -p "$MODULES_DIR" "$DEMO_DIR" "$RELEASE"

# The version the manifest declares. `module add` refuses a release whose
# manifest says another one, so reading it here is the only way the two agree
# without being kept in step by hand.
QUEUE_VERSION=$(sed -n 's/^version:[ 	]*//p' "$MODULE_DIR/module.yaml" | head -1)
if [ -z "$QUEUE_VERSION" ]; then
	echo "FAIL  $MODULE_DIR/module.yaml declares no version"
	exit 1
fi

echo " 0. the print queue module declares version $QUEUE_VERSION"

# ── The Go half, inside its own folder ────────────────────────────────
# The module carries its own go.mod, so the core's `go build ./...` walks past
# it. These three are the build a person who cloned the module alone runs.
step "the Go half builds and vets in its own folder" sh -c \
	"cd '$MODULE_DIR' && go build ./... && go vet ./..."
step "the Go half's rules, routes, manifest and changelog hold" sh -c \
	"cd '$MODULE_DIR' && go test -count=1 ./..."

# ── The two packages the browser half links to ───────────────────────
if [ ! -f "$LIBRARY_DIR/dist/build/module.js" ]; then
	step "the component library builds" sh -c \
		"cd '$LIBRARY_DIR' && pnpm install --frozen-lockfile && pnpm run build"
fi

if [ ! -f "$CLIENT_SDK_DIR/dist/index.js" ]; then
	step "the client SDK builds" sh -c \
		"cd '$CLIENT_SDK_DIR' && pnpm install --frozen-lockfile && pnpm run build"
fi

# ── The typed client ─────────────────────────────────────────────────
# It is generated from the same fragment the module embeds, so -check fails
# when the fragment moved and nobody regenerated.
step "the typed client is what the fragment generates" \
	go run ./cmd/generate-client-sdk -module -check \
	-document "$MODULE_DIR/openapi.json" \
	-namespace "$QUEUE_NAMESPACE" -name simple-queue-client -out "$CLIENT_DIR"

step "the typed client installs" sh -c "cd '$CLIENT_DIR' && pnpm install --frozen-lockfile"
step "the client types match the fragment" sh -c \
	"cd '$CLIENT_DIR' && pnpm run generate:types && git diff --exit-code src/generated/"
step "the typed client builds" sh -c "cd '$CLIENT_DIR' && pnpm run build"

# ── The browser half ─────────────────────────────────────────────────
step "the browser half installs" sh -c "cd '$UI_DIR' && pnpm install --frozen-lockfile"

# The browser tests run in the Chromium of the browser half's own playwright,
# so that is the copy installed. On a GitHub runner, which is Linux, the
# libraries Chromium needs are installed with it.
PLAYWRIGHT_FLAGS=
if [ "${GITHUB_ACTIONS:-}" = true ] && [ "$(uname -s)" = Linux ]; then
	PLAYWRIGHT_FLAGS=--with-deps
fi
step "Chromium for the browser tests installs" sh -c \
	"cd '$UI_DIR' && pnpm exec playwright install $PLAYWRIGHT_FLAGS chromium"
step "the browser half lints, type-checks and its tests pass in Chromium" sh -c \
	"cd '$UI_DIR' && pnpm run check"
step "the browser half's Storybook builds" sh -c "cd '$UI_DIR' && pnpm run build-storybook"
step "the browser half builds into a bundle" sh -c "cd '$UI_DIR' && pnpm run build"

if [ ! -f "$UI_DIR/dist/module.yaml" ] || [ ! -f "$UI_DIR/dist/ui/index.js" ]; then
	echo "FAIL  $UI_DIR/dist is missing module.yaml or ui/index.js"
	exit 1
fi

# ── The release ──────────────────────────────────────────────────────
# The layout `module add` reads: the manifest at the root, this platform's
# binary beside it, the built bundle under ui/, and a checksums.txt over all of
# them. The file name of the binary is <name>_<GOOS>_<GOARCH>, which is what
# the host looks for in the directory the declaration points at.
BINARY="simple-queue_$(go env GOOS)_$(go env GOARCH)$(go env GOEXE)"

pack() {
	# The output path is relative to the module folder. An absolute one would
	# be this shell's own spelling of it, and under Git Bash on Windows that
	# is a path the Go toolchain cannot open.
	( cd "$MODULE_DIR" && go build -o "../../$WORK_ROOT/$BINARY" . ) || return 1
	sh extensions/scripts/pack-release.sh "$UI_DIR/dist" "$WORK_ROOT/$BINARY" "$RELEASE"
}

step "the release packs" pack

if [ ! -f "$RELEASE/module.yaml" ] || [ ! -f "$RELEASE/ui/index.js" ] || [ ! -f "$RELEASE/$CHECKSUMS_FILE" ]; then
	echo "FAIL  $RELEASE is missing module.yaml, ui/index.js or checksums.txt"
	exit 1
fi

# ── The operator's file ──────────────────────────────────────────────
# It names where the machine-owned overlay goes and where modules are
# installed. The module keeps its queues in a database of its own, which the
# install grants: a SQLite file under the modules directory.
#
# Every path here is relative to the directory the server is started from,
# which is the repository root.
cat > "$OUT_CONFIG" <<- YAML
	core:
	  overlay: $DEMO_DIR/resources.json
	  modules: $MODULES_DIR
YAML

CORE=$WORK_ROOT/prusa-connect$(go env GOEXE)
# The binary is not called demo: $DEMO_DIR is $WORK_ROOT/demo, and on Linux,
# where GOEXE is empty, a binary of that name and the directory would be one
# path.
DEMO=$WORK_ROOT/demo-server$(go env GOEXE)

# Both are stamped with the version `make build` stamps. The module's core
# range is held against it, and an unstamped build reads a version Go made up
# from the commit, which no range names.
STAMP="-X github.com/prusa3d/prusa-connect/internal/kernel/version.stamped=$(sh scripts/build-version.sh)"

step "the core and the demo build" sh -c \
	"go build -ldflags '$STAMP' -o '$CORE' ./cmd/prusa-connect && \
	 go build -ldflags '$STAMP' -o '$DEMO' ./cmd/demo"

# -approve grants what module.yaml asks for: the viewer role, which reaches
# observe and read_storage. Starting a print by itself needs operate on the
# printer, which no install grants: a person writes it later, the way the
# Queue tab's button does.
step "the print queue module installs" "$CORE" module add "$QUEUE_MODULE@$QUEUE_VERSION" \
	-config "$OUT_CONFIG" -from "$RELEASE" -approve
step "the install granted a SQLite database" grep -q '"engine": *"sqlite"' "$DEMO_DIR/resources.json"

step "the listing on disk holds one line for it" sh -c \
	"'$CORE' module list -config '$OUT_CONFIG' | tee '$WORK/list.txt' && \
	 grep -q '$QUEUE_NAMESPACE' '$WORK/list.txt'"


# ── The demo ─────────────────────────────────────────────────────────
port_free() {
	curl -sS -o /dev/null --max-time 2 "http://127.0.0.1:$1/" > /dev/null 2>&1
	[ "$?" -eq 7 ]
}

pick_port() {
	port=$FIRST_PORT
	while [ "$port" -lt $((FIRST_PORT + 50)) ]; do
		if port_free "$port"; then
			DEMO_ADDR=127.0.0.1:$port
			BASE_URL=http://$DEMO_ADDR
			return 0
		fi
		port=$((port + 1))
	done

	echo "no free port from $FIRST_PORT to $((FIRST_PORT + 49)) on 127.0.0.1" >&2
	return 1
}

step "a free port for the demo" pick_port
echo "    the demo binds $DEMO_ADDR"

start_demo() {
	# -idle leaves the printer to the queue, and -print-gap makes a print
	# take seconds instead of minutes.
	"$DEMO" -config "$OUT_CONFIG" -dir "$DEMO_DIR" -addr "$DEMO_ADDR" \
		-printers 1 -flap 0 -cameras=false -idle -print-gap 2s >> "$WORK/demo.log" 2>&1 &
	DEMO_PID=$!
	echo "$DEMO_PID" > "$LOCK/demo.pid"
}

stop_demo() {
	[ -n "$DEMO_PID" ] || return 0
	kill "$DEMO_PID" 2> /dev/null
	wait "$DEMO_PID" 2> /dev/null
	DEMO_PID=
	rm -f "$LOCK/demo.pid"
}

ready() {
	waited=0
	while [ "$waited" -lt 120 ]; do
		if ! kill -0 "$DEMO_PID" 2> /dev/null; then
			echo "the demo ended before it served" >&2
			cat "$WORK/demo.log" >&2
			return 1
		fi

		if curl -fsS "$BASE_URL/api/v1/health" > /dev/null 2>&1; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/demo.log" >&2
	return 1
}

read_password() {
	waited=0
	while [ "$waited" -lt 60 ]; do
		PASSWORD=$(sed -n 's/^ *password  *//p' "$WORK/demo.log" | head -1)
		if [ -n "$PASSWORD" ]; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/demo.log" >&2
	return 1
}

sign_in() {
	waited=0
	while [ "$waited" -lt 60 ]; do
		rm -f "$WORK/cookies"

		if curl -fsS -c "$WORK/cookies" -X POST "$BASE_URL/api/v1/auth/login" \
			-H 'content-type: application/json' \
			-d "{\"username\":\"demo\",\"password\":\"$PASSWORD\"}" > /dev/null 2>&1; then
			return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	echo "the demo account did not sign in with the password the demo printed" >&2
	return 1
}

# one_a_line puts each object of a JSON list on a line of its own. An entry
# holds no object inside it, so every },{ is a boundary.
one_a_line() {
	awk '{ gsub(/\},\{/, "}\n{"); print }'
}

# queue reads the queue of the printer into $WORK/queue.json, and its ended
# prints, which have a route of their own, into $WORK/history.json.
queue() {
	curl -fsS -b "$WORK/cookies" "$BASE_URL$MOUNT/printers/$DEVICE" > "$WORK/queue.json" &&
		curl -fsS -b "$WORK/cookies" "$BASE_URL$MOUNT/printers/$DEVICE/history" > "$WORK/history.json"
}

# history is the ended prints of the queue last read, one object a line.
history() {
	sed -n 's/.*"history":\[\(.*\)\]}.*/\1/p' "$WORK/history.json" | one_a_line
}

# entries is the waiting and the active files of the queue last read.
entries() {
	sed -n 's/.*"entries":\[\(.*\)\],"printer".*/\1/p' "$WORK/queue.json" | one_a_line
}

# printer_listed waits until the module lists a connected Buddy printer, one
# that can be marked ready, and keeps its id. The demo also connects a printer
# of another kind, which takes no such command.
printer_listed() {
	waited=0
	while [ "$waited" -lt 60 ]; do
		if curl -fsS -b "$WORK/cookies" "$BASE_URL$MOUNT/queues/summary" > "$WORK/printers.json" 2> /dev/null; then
			DEVICE=$(awk '{ gsub(/\},\{"device_id"/, "}\n{\"device_id\""); print }' "$WORK/printers.json" |
				grep '"connected":true' | grep '"can_be_ready":true' |
				sed -n 's/.*"device_id":"\([^"]*\)".*/\1/p' | head -1)
			[ -n "$DEVICE" ] && return 0
		fi

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/printers.json" >&2
	tail -n 40 "$WORK/demo.log" >&2
	return 1
}

# upload writes a small G-code file into the demo's storage as the signed-in
# person. The demo answers before its storage is ready, and until then the
# core answers a 503 it documents as retryable.
upload() {
	printf '; %s, printed by the gate\nG28\nG1 X10 Y10 F3000\n' "$1" > "$WORK/$1.gcode"
	tries=0
	while :; do
		tries=$((tries + 1))
		status=$(curl -sS -o "$WORK/upload.json" -w '%{http_code}' -b "$WORK/cookies" \
			-X PUT "$BASE_URL/api/v1/storages/local/content?path=gate/$1.gcode" \
			-H 'content-type: application/octet-stream' --data-binary @"$WORK/$1.gcode")
		[ "$status" = 200 ] || [ "$status" = 201 ] && return 0
		if [ "$status" != 503 ] || [ "$tries" -ge 10 ]; then
			echo "the upload of $1 answered $status after $tries attempt(s):" >&2
			cat "$WORK/upload.json" >&2
			return 1
		fi
		sleep 1
	done
}

uploads() {
	upload first && upload second
}

# add puts a file on the queue of the printer.
add() {
	status=$(curl -sS -o "$WORK/add.json" -w '%{http_code}' -b "$WORK/cookies" \
		-X POST "$BASE_URL$MOUNT/printers/$DEVICE/entries" -H 'content-type: application/json' \
		-d "{\"storage\":\"local\",\"path\":\"gate/$1.gcode\"}")
	[ "$status" = 201 ] || { echo "adding $1 answered $status:" >&2; cat "$WORK/add.json" >&2; return 1; }
}

adds() {
	add first && add second
}

# both_wait holds that the queue starts nothing on an idle printer whose bed
# nobody marked clear, and that it may not start a print by itself yet.
both_wait() {
	sleep 3
	queue || return 1
	[ "$(entries | grep -c '"state":"waiting"')" = 2 ] &&
		grep -q '"starts_alone":false' "$WORK/queue.json" ||
		{ echo "the queue did not hold two waiting files:" >&2; cat "$WORK/queue.json" >&2; return 1; }
}

# grant lets the module operate the printer, which is what the Queue tab's
# button writes.
grant() {
	status=$(curl -sS -o "$WORK/grant.json" -w '%{http_code}' -b "$WORK/cookies" \
		-X POST "$BASE_URL/api/v1/grants/device/$DEVICE" -H 'content-type: application/json' \
		-d '{"relation":"operator","subject":"sink:simple-queue"}')
	[ "$status" = 200 ] || [ "$status" = 201 ] ||
		{ echo "the grant answered $status:" >&2; cat "$WORK/grant.json" >&2; return 1; }
	queue && grep -q '"starts_alone":true' "$WORK/queue.json" ||
		{ echo "the queue does not start prints by itself after the grant:" >&2; cat "$WORK/queue.json" >&2; return 1; }
}

# mark_ready says the bed is clear.
mark_ready() {
	status=$(curl -sS -o "$WORK/ready.json" -w '%{http_code}' -b "$WORK/cookies" \
		-X POST "$BASE_URL$MOUNT/printers/$DEVICE/ready" -H 'content-type: application/json' -d '{"ready":true}')
	[ "$status" = 200 ] || { echo "marking the bed clear answered $status:" >&2; cat "$WORK/ready.json" >&2; return 1; }
}

# finished waits until the file has ended on the queue as finished, and
# holds who started it: nobody for the queue itself, the demo account else.
finished() {
	waited=0
	while [ "$waited" -lt 90 ]; do
		queue || return 1
		line=$(history | grep "\"name\":\"$1.gcode\"")
		case $line in
		*'"outcome":"finished"'*)
			case $2 in
			queue) case $line in *'"started_by"'*) break ;; esac ;;
			person) case $line in *'"started_by":"user:'*) ;; *) break ;; esac ;;
			esac
			return 0
			;;
		*'"outcome":'*) break ;;
		esac

		waited=$((waited + 1))
		sleep 1
	done

	cat "$WORK/queue.json" >&2
	tail -n 40 "$WORK/demo.log" >&2
	return 1
}

# second_waits holds that the queue does not start the next file on a printer
# that just finished: the bed holds the last print until a person clears it.
# The wait is longer than the module's sweep, so a start would have happened.
second_waits() {
	sleep 12
	queue || return 1
	entries | grep '"name":"second.gcode"' | grep -q '"state":"waiting"' &&
		[ "$(entries | grep -c '"state":"sending"\|"state":"printing"')" = 0 ] ||
		{ echo "the second file did not wait for a person:" >&2; cat "$WORK/queue.json" >&2; return 1; }
}

# print_next starts the next file as the signed-in person.
print_next() {
	status=$(curl -sS -o "$WORK/start.json" -w '%{http_code}' -b "$WORK/cookies" \
		-X POST "$BASE_URL$MOUNT/printers/$DEVICE/start")
	[ "$status" = 200 ] || { echo "Print next answered $status:" >&2; cat "$WORK/start.json" >&2; return 1; }
}

# scoped mints a token with one scope for the demo account.
scoped() {
	curl -fsS -b "$WORK/cookies" -X POST "$BASE_URL/api/v1/auth/credentials" -H 'content-type: application/json' \
		-d "{\"name\":\"gate $1\",\"scopes\":[\"$1\"]}" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
}

# scopes holds the routes to the scope of the credential: a read token reads
# the queues and changes none, and an operate token adds a file and takes it
# off again.
scopes() {
	READ_TOKEN=$(scoped read)
	OPERATE_TOKEN=$(scoped operate)
	[ -n "$READ_TOKEN" ] && [ -n "$OPERATE_TOKEN" ] || return 1

	status=$(curl -sS -o /dev/null -w '%{http_code}' -H "authorization: Bearer $READ_TOKEN" "$BASE_URL$MOUNT/queues/summary")
	[ "$status" = 200 ] || { echo "a read token listing the queues answered $status" >&2; return 1; }

	body='{"storage":"local","path":"gate/first.gcode"}'
	status=$(curl -sS -o "$WORK/scoped.json" -w '%{http_code}' -H "authorization: Bearer $READ_TOKEN" \
		-X POST "$BASE_URL$MOUNT/printers/$DEVICE/entries" -H 'content-type: application/json' -d "$body")
	[ "$status" = 403 ] || { echo "a read token adding answered $status" >&2; cat "$WORK/scoped.json" >&2; return 1; }

	status=$(curl -sS -o "$WORK/scoped.json" -w '%{http_code}' -H "authorization: Bearer $OPERATE_TOKEN" \
		-X POST "$BASE_URL$MOUNT/printers/$DEVICE/entries" -H 'content-type: application/json' -d "$body")
	[ "$status" = 201 ] || { echo "an operate token adding answered $status" >&2; cat "$WORK/scoped.json" >&2; return 1; }

	# The file printed already, so this copy waits on a printer that finished.
	ENTRY=$(sed -n 's/.*"entries":\[{[^{}]*"id":"\([^"]*\)".*/\1/p' "$WORK/scoped.json")
	[ -n "$ENTRY" ] || { cat "$WORK/scoped.json" >&2; return 1; }
	status=$(curl -sS -o "$WORK/scoped.json" -w '%{http_code}' -H "authorization: Bearer $OPERATE_TOKEN" \
		-X DELETE "$BASE_URL$MOUNT/printers/$DEVICE/entries/$ENTRY")
	[ "$status" = 200 ] || { echo "an operate token removing answered $status" >&2; cat "$WORK/scoped.json" >&2; return 1; }
}

start_demo
step "the demo serves" ready
step "the demo prints an account to sign in with" read_password
step "the demo account signs in" sign_in
step "the module lists the simulated printer" printer_listed
echo "    the printer is $DEVICE"

step "the listing publishes the bundle under the same namespace" sh -c \
	"curl -fsS -b '$WORK/cookies' '$BASE_URL/api/v1/resources' > '$WORK/resources.json' && \
	 grep -q '\"entry_url\":\"$MOUNT/ui/ui/index.js\"' '$WORK/resources.json'"

step "the bundle answers" sh -c \
	"curl -fsS -o /dev/null -b '$WORK/cookies' '$BASE_URL$MOUNT/ui/ui/index.js'"

step "two G-code files are uploaded into the demo's storage" uploads
step "both files go on the printer's queue" adds
step "both wait on an idle printer nobody marked ready" both_wait
step "a grant lets the queue start prints on the printer" grant
step "the bed is marked clear" mark_ready
step "the first file starts by itself and finishes" finished first queue
step "the second file waits for a person after it" second_waits
step "Print next starts the second file" print_next
step "the second file finishes, started by the person" finished second person
step "a read token reads, and an operate token adds and removes" scopes

stop_demo

echo "OK    the print queue module passed $STEPS steps"
echo
echo "      the release     $RELEASE"
echo "      installed into  $MODULES_DIR"
echo "      the config      $OUT_CONFIG"

}
main "$@"
