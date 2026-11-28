# Remus STEP translator

These files are the `remus-wasm-io` package of
[Remus](https://github.com/esaueng/remus), copied byte for byte. The Prepare
editor reads STEP files with it, in `src/views/prepare-panel/step-remus.ts`.

| file | where it comes from |
|---|---|
| `LICENSE-APACHE`, `NOTICE`, `remus_wasm_io.d.ts`, `remus_wasm_io_bg.js`, `remus_wasm_io_bg.wasm` | `crates/wasm-io/pkg` at the commit `SOURCE` names, unchanged |
| `SHA256SUMS` | the SHA-256 of each of those five files |
| `SOURCE` | the repository, the commit and the folder |
| `package.json`, `glue.d.ts`, `README.md` | written here. `package.json` declares no scripts |

`package.json` exports the glue file as `remus-wasm-io/glue`, with its
types in `glue.d.ts`, and the WebAssembly.

## Why a copy and not a dependency

Remus publishes no npm package. A git dependency on its repository makes pnpm
prepare the whole repository first, which runs the repository's own scripts.
`pnpm-workspace.yaml` refuses every dependency script, as
`.agents/rules/security-supply-chain.md` requires, so a clean install stops
with `ERR_PNPM_GIT_DEP_PREPARE_NOT_ALLOWED`. `package.json` names this folder
with `link:`, and pnpm runs nothing from it.

## Checks

The repository's `.gitattributes` marks every file here `-text`, so git
converts no line ending and a checkout holds the bytes `SHA256SUMS` describes.

`pnpm run check` runs `node scripts/remus-vendor.mjs check`, which needs no
network, and `scripts/remus-vendor.test.mjs`. The check fails when a file
differs from `SHA256SUMS` or when `SHA256SUMS` or `SOURCE` departs from its
exact format. It also fails when this folder holds a link or a file it does
not list.
`node scripts/remus-vendor.mjs verify` reads each file here once, holds it to
`SHA256SUMS`, and compares the same bytes with the file at the commit in
`SOURCE` on GitHub.

## Update

`scripts/remus-vendor.mjs` writes nothing. To copy another commit, from
`extensions/server-slicing/ui` on a clean checkout:

1. Choose a commit of Remus `main` whose `crates/wasm-io/pkg` holds the change
   you need, as 40 lowercase hex characters.
2. Download the five files over the copies here:

   ```sh
   commit=<40 hex>
   for f in LICENSE-APACHE NOTICE remus_wasm_io.d.ts remus_wasm_io_bg.js remus_wasm_io_bg.wasm; do
     curl -fsSL -o "vendor/remus-wasm-io/$f" \
       "https://raw.githubusercontent.com/esaueng/remus/$commit/crates/wasm-io/pkg/$f"
   done
   printf 'esaueng/remus %s crates/wasm-io/pkg\n' "$commit" > vendor/remus-wasm-io/SOURCE
   node scripts/remus-vendor.mjs print-sums > vendor/remus-wasm-io/SHA256SUMS
   ```

3. Set `version` in `package.json` to the version in
   `crates/wasm-io/pkg/package.json` at that commit.
4. Run `node scripts/remus-vendor.mjs verify`, `pnpm run check` and the STEP
   tests. Commit, and name the Remus commit in the commit body.

If anything goes wrong,
`git restore --source=HEAD --staged --worktree -- vendor/remus-wasm-io` puts
the committed copy back, staged changes included.
