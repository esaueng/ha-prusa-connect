#!/bin/sh
set -eu

usage() {
    echo "Usage: $0 <amd64|aarch64> <output directory>" >&2
    exit 2
}
[ "$#" -eq 2 ] || usage
case "$1" in
    amd64) docker_arch=amd64 ;;
    aarch64) docker_arch=arm64 ;;
    *) usage ;;
esac

mkdir -p "$2"
output=$(cd "$2" && pwd -P)
cd "$(dirname "$0")/.."

staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT
app=ha-prusa-connect
app_source=deploy/home-assistant/$app
package=$staging/$app
mkdir "$package"
cp "$app_source/Dockerfile" "$app_source/README.md" "$app_source/run.sh" "$app_source/server.yaml" LICENSE "$package/"
sed "s/^arch: .*/arch: [$1]/" "$app_source/config.yaml" > "$package/config.yaml"

version=$(sh scripts/build-version.sh)
docker buildx build --platform "linux/$docker_arch" --target home-assistant-files \
    --file docker/server/Dockerfile --build-arg "BUILD_VERSION=$version" \
    --output "type=local,dest=$package/rootfs" .

archive=$app-$1.tar.gz
COPYFILE_DISABLE=1 tar -czf "$staging/$archive" -C "$staging" "$app"
mv "$staging/$archive" "$output/$archive"
printf '%s\n' "$output/$archive"
