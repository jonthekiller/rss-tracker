# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM node:24-slim AS build
WORKDIR /app

# Build tools for better-sqlite3 if no prebuilt binary matches the platform
# (kept in the build stage only — never in the runtime image).
RUN apt-get update \
  && apt-get install -y --no-install-recommends python3 make g++ \
  && rm -rf /var/lib/apt/lists/*

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src
RUN npm run build && npm prune --omit=dev

# ---- Runtime stage ----
FROM node:24-slim AS runtime
ENV NODE_ENV=production
WORKDIR /app

# dumb-init: proper PID 1 signal handling. Security upgrade of the base
# Debian packages so each build ships the latest published patches.
RUN apt-get update \
  && apt-get upgrade -y \
  && apt-get install -y --no-install-recommends dumb-init \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./

# Readable by any runtime UID (the container may run as the host user).
RUN chmod -R a+rX /app \
  && groupadd -r bot && useradd -r -g bot bot \
  && mkdir -p /app/data && chown -R bot:bot /app

USER bot
VOLUME ["/app/data"]

# start-period covers Discord login on restart: with an existing database,
# checks before the bot is ready would otherwise count as failures.
HEALTHCHECK --interval=60s --timeout=10s --start-period=120s --retries=3 \
  CMD ["node", "dist/healthcheck.js"]

ENTRYPOINT ["dumb-init", "--"]
CMD ["node", "dist/index.js"]
