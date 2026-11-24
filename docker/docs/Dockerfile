# syntax=docker/dockerfile:1

# The documentation site, built and served the way it is deployed: a static
# directory under nginx, at the /docs base every link in the content assumes.
#
# Build from the repository root, because the site reads three files outside
# docs/: the OpenAPI document the swagger page bundles and the two AsyncAPI
# documents the reference pages are generated from.
#
#   docker build -f docker/docs/Dockerfile -t prusa-connect-docs:local .
#
# The build copies docs/ in rather than mounting it. A bind mount is not what a
# deployment does, and the Docker Desktop on the machine this repository is
# developed on refuses every bind mount with an input/output error.

# Digest-pinned, the same tag and digest as docker/server/Dockerfile, so both
# images build on the same Node. Bump the two together
# (`docker buildx imagetools inspect node:24-alpine`).
FROM node:24-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53 AS site-builder
ENV PNPM_HOME=/pnpm
ENV PATH="$PNPM_HOME:$PATH"
# Resolves the pnpm version from the packageManager field in docs/package.json.
RUN corepack enable
WORKDIR /site
# Manifest, lockfile and workspace settings first, so the install layer is
# reused until a dependency changes. pnpm-workspace.yaml carries the settings
# the lockfile was built with, and omitting it makes --frozen-lockfile fail.
COPY docs/package.json docs/pnpm-lock.yaml docs/pnpm-workspace.yaml ./docs/
RUN cd docs && pnpm install --frozen-lockfile
# The three documents the site reads from outside its own directory. Copied
# under the same relative paths, because docs/scripts resolve them from docs/.
# All three are frozen captures of the retired server, which outlived the code
# they describe (ADR-0013 rule 8).
COPY internal/arch/testdata/retired-openapi3.json ./internal/arch/testdata/
COPY internal/arch/testdata/shipping-sse-asyncapi.yaml ./internal/arch/testdata/
COPY adapters/events/mqtt/testdata/shipping-asyncapi.yaml ./adapters/events/mqtt/testdata/
# The root .dockerignore keeps docs/node_modules, docs/dist and docs/out out of
# the context, so the install above is the only node_modules in the image.
COPY docs ./docs
# build:static is the deploy build. It lays the site out under out/docs/ and
# drops a redirect at out/index.html, so the directory serves at a domain root
# with every /docs/* link resolving. The base is fixed in astro.config.mjs and
# is not a build argument here, because a base that moved would leave the
# hand-written links in the content pointing at the old one.
RUN cd docs && pnpm run build:static

# Digest-pinned. ghcr.io/nginx/nginx-unprivileged:1.30.8-alpine at the time of
# pinning. The unprivileged variant listens on 8080, runs as uid 101 and keeps
# its pid file and temp paths under /tmp, so the root filesystem can be
# read-only with one tmpfs.
FROM ghcr.io/nginx/nginx-unprivileged:1.30.8-alpine@sha256:637c35e46af7bc0dd3d3bcf7162e004e13ab3b370fde7cd416e2ac410b5a985a

# The one server block, over the image's own. It serves the built directory,
# answers a missing page with the site's 404 page, and keeps a redirect
# relative so the published host port survives it.
COPY --chown=101:101 docker/docs/config/default.conf /etc/nginx/conf.d/default.conf
COPY --chown=101:101 --from=site-builder /site/docs/out /usr/share/nginx/html

USER 101

EXPOSE 8080

# Fetches the site's index through the wget the image ships, so the probe
# needs no extra layer. The loopback address is written out because localhost
# may resolve to ::1 first and the entrypoint cannot add the IPv6 listener on a
# read-only root.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/docs/"]
