# syntax=docker/dockerfile:1

# ---- Build stage ----
FROM node:24-slim AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src
RUN npm run build && npm prune --omit=dev

# ---- Runtime stage ----
FROM node:24-slim AS runtime
ENV NODE_ENV=production
WORKDIR /app

# dumb-init: proper PID 1 signal handling
RUN apt-get update \
  && apt-get install -y --no-install-recommends dumb-init \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./

RUN groupadd -r bot && useradd -r -g bot bot \
  && mkdir -p /app/data && chown -R bot:bot /app

USER bot
VOLUME ["/app/data"]

HEALTHCHECK --interval=60s --timeout=10s --start-period=30s --retries=3 \
  CMD ["node", "dist/healthcheck.js"]

ENTRYPOINT ["dumb-init", "--"]
CMD ["node", "dist/index.js"]
