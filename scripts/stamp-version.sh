#!/bin/bash
# Writes the core's VERSION into every file that repeats it, so the gates that
# compare them with VERSION hold after a release. scripts/release.sh runs it
# after it writes VERSION, and stages what changed.
#
#   packages/typescript/package.json          the SDK's version
#   examples/*/client, extensions/parts/client, extensions/materials/client
#                                             each module client's peer on the SDK
#   the same clients' README.md               the sentence that names that peer
#   packages/svelte, packages/standards       their version, and the library's peer
#   packages/svelte/src/version.ts            the version the module loader checks
#   cmd/new-module/published.go               the version a new module asks for
#
# Usage: scripts/stamp-version.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION=$(tr -d '[:space:]' < VERSION)

# The SDK and the service-plugin example client, as `make generate-client-sdk`
# writes them.
make generate-client-sdk >/dev/null

# The other module clients, generated the way their gates check them.
while read -r module namespace name; do
	go run ./cmd/generate-client-sdk -module \
		-document "$module/openapi.json" \
		-namespace "$namespace" -name "$name" -out "$module/client"
done <<'MODULES'
examples/spools com.github.aux.acme.spools spools-client
extensions/parts com.github.aux.prusa3d.parts parts-client
extensions/materials com.github.aux.prusa3d.materials materials-client
extensions/buddy-debug-logs com.github.aux.prusa3d.buddy-debug-logs buddy-debug-logs-client
extensions/threemf com.github.aux.prusa3d.threemf threemf-client
extensions/server-slicing com.github.aux.prusa3d.server-slicing server-slicing-client
extensions/simple-queue com.github.aux.prusa3d.simple-queue simple-queue-client
MODULES

# A module client the list above leaves out would keep the old version, and
# its gate would refuse the release tree.
for client in examples/*/client/openapi.json extensions/*/client/openapi.json; do
	module=${client%/client/openapi.json}
	[ "$module" = examples/service-plugin ] && continue
	grep -q "^$module " "$0" || { echo "stamp-version.sh does not regenerate $module/client: add it to the list" >&2; exit 1; }
done

# A module client's README is the author's file, so the generator leaves it
# alone, but its peer sentence names the core's version.
for readme in examples/*/client/README.md extensions/*/client/README.md; do
	sed -E -i.bak "s/(The peer range on \`@prusa\/connect\` is \`)[^\`]+(\`)/\1${VERSION}\2/" "$readme"
	rm -f "$readme.bak"
done

# The component library and the shared standards ship at the core's version,
# and the library peers the SDK at that exact version.
node -e '
const fs = require("node:fs");
const version = process.argv[1];
for (const file of ["packages/svelte/package.json", "packages/standards/package.json"]) {
  const manifest = JSON.parse(fs.readFileSync(file, "utf8"));
  manifest.version = version;
  if (manifest.peerDependencies?.["@prusa/connect"]) {
    manifest.peerDependencies["@prusa/connect"] = version;
  }
  fs.writeFileSync(file, JSON.stringify(manifest, null, 2) + "\n");
}
' "$VERSION"

# The loader checks a module's `library` range against this constant.
sed -E -i.bak "s/^(export const LIBRARY_VERSION = ')[^']+(';)/\1${VERSION}\2/" packages/svelte/src/version.ts
rm -f packages/svelte/src/version.ts.bak
grep -q "LIBRARY_VERSION = '${VERSION}'" packages/svelte/src/version.ts || {
	echo "stamp-version: packages/svelte/src/version.ts no longer has the LIBRARY_VERSION line this script rewrites" >&2
	exit 1
}

# A module written outside a checkout asks for the packages at this version.
sed -E -i.bak "s/^(\tpublishedPackages = \")[^\"]+(\")/\1${VERSION}\2/" cmd/new-module/published.go
rm -f cmd/new-module/published.go.bak
grep -q "publishedPackages = \"${VERSION}\"" cmd/new-module/published.go || {
	echo "stamp-version: cmd/new-module/published.go no longer has the publishedPackages line this script rewrites" >&2
	exit 1
}

echo "✓ Stamped ${VERSION} into the client packages"
