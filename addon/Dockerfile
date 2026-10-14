# syntax=docker/dockerfile:1

# Home Assistant add-on image. Built by .github/workflows/docker-publish.yml on
# version tags and pushed to jonthekiller/rss-tracker-addon-{amd64,aarch64}.
# It reuses the main application image and only overrides the entrypoint:
# `bot addon-run` reads /data/options.json (written by the Supervisor) and
# starts the bot — no shell, no Node needed.

ARG VERSION=latest
FROM jonthekiller/rss-tracker:${VERSION}

# Run as root: the Supervisor mounts /data and the add-on must be able to
# write its SQLite database and backups there.
USER root

# Baked into the image so the Docker healthcheck (which only sees the static
# container env, not the options translated at runtime) reads the same
# database as the bot process. addon-run uses the same value.
ENV DB_PATH=/data/rss-tracker.db

CMD [ "/app/bot", "addon-run" ]
