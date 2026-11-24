# The Keycloak fixture's image: Keycloak with this project's test realm baked
# in, so `start-dev --import-realm` finds it without a bind mount
# (docker-services.md says why there is none).
#
# Pinned by digest. quay.io/keycloak/keycloak 26.8.2-3 at the time of pinning.
FROM quay.io/keycloak/keycloak:26.8.2-3@sha256:7e66a96e4739239070aeaf72e4c5decf0f7f682cfc725b9f7ea2b336eaba38b3

# Owned by the uid the image runs as, so the import reads the file whatever
# the host's umask was when it was checked out.
COPY --chown=1000:0 config/pcl-realm.json /opt/keycloak/data/import/pcl-realm.json
