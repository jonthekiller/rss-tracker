# syntax=docker/dockerfile:1

# Home Assistant add-on image. Built by .github/workflows/docker-publish.yml on
# version tags and pushed to jonthekiller/rss-tracker-addon-{amd64,aarch64}.
# It reuses the main application image and only adds the Home Assistant
# entrypoint (reads /data/options.json written by the Supervisor).

ARG VERSION=latest
FROM jonthekiller/rss-tracker:${VERSION}

COPY --chmod=0755 run.sh export-options.js /usr/local/bin/

# Run as root: the Supervisor mounts /data and the add-on must be able to
# write its SQLite database and backups there.
USER root

# Baked into the image so the Docker healthcheck (which only sees the static
# container env, not the env exported by run.sh) reads the same database as
# the bot process. run.sh exports the same value at runtime.
ENV DB_PATH=/data/rss-tracker.db

CMD [ "/usr/local/bin/run.sh" ]
