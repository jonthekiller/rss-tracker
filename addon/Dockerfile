# syntax=docker/dockerfile:1

# Home Assistant add-on build. The add-on directory must contain ./app
# with the application source (run scripts/prepare-addon.sh first).

FROM node:24-slim AS build
WORKDIR /app

COPY app/package.json app/package-lock.json ./
RUN npm ci

COPY app/tsconfig.json ./
COPY app/src ./src
RUN npm run build && npm prune --omit=dev

FROM node:24-slim
ENV NODE_ENV=production
WORKDIR /app

RUN apt-get update \
  && apt-get install -y --no-install-recommends dumb-init \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY app/package.json ./
COPY run.sh export-options.js /usr/local/bin/
RUN chmod +x /usr/local/bin/run.sh \
  && mkdir -p /data && chown -R node:node /app /data

# Home Assistant add-on labels
LABEL io.hass.type="addon" \
      io.hass.arch="${TARGETARCH:-amd64}"

USER node
CMD [ "/usr/local/bin/run.sh" ]
