#!/bin/sh
set -eu

# Supervisor mounts /data from the host with root ownership.
install -d -m 0750 -o prusa -g prusa /data/prusa-connect
cd /data/prusa-connect

exec setpriv --reuid=prusa --regid=prusa --clear-groups --no-new-privs sh -eu -c '
    umask 077
    if [ ! -e config.yaml ]; then
        cp /usr/local/share/ha-prusa-connect/server.yaml config.yaml
    fi

    exec /prusa_connect core -config config.yaml -addr :8080
'
