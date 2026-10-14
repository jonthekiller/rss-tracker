# <img src="icon.png" width="36" alt="RSS-Tracker icon" /> RSS-Tracker

[![CI](https://github.com/jonthekiller/rss-tracker/actions/workflows/ci.yml/badge.svg)](https://github.com/jonthekiller/rss-tracker/actions/workflows/ci.yml)
[![Docker Hub](https://img.shields.io/docker/v/jonthekiller/rss-tracker?sort=semver&label=docker)](https://hub.docker.com/r/jonthekiller/rss-tracker)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A lightweight self-hosted Discord bot for watching RSS feeds — a minimalist alternative to MonitoRSS.

**Features:**

- 🤖 One bot for X channels/servers — each feed has its own configuration
- 📰 Customizable templates (`{title}`, `{link}`, `{description}`, `{summary}`, `{author}`, `{date}`, `{image}`, `{feed.name}`) + optional JSON embeds
- 📚 Burst handling: when a feed publishes X articles at once, a grouped **digest** is sent past a threshold (`auto` mode), or always / never (`digest` / `individual`)
- 🔎 Include/exclude filters by keyword or regex (title, description)
- ⏱️ Per-feed check frequency (default 10 min), conditional GETs (ETag/Last-Modified), exponential backoff on failure
- 🔔 Failure alerts: DM or message when a feed goes down (3 consecutive failures)
- 🔒 Per-channel permissions: anyone who can write in a channel can configure its feeds (server admins: full access)
- 💾 Embedded SQLite (no MongoDB/Redis), **MonitoRSS** import and full backup/restore **from Discord**
- 🐳 Docker image ~250 MB, ~100–150 MB RAM, near-zero CPU outside checks

## Installation (Docker)

Image published on [Docker Hub](https://hub.docker.com/r/jonthekiller/rss-tracker) (multi-arch `amd64` / `arm64`):

```bash
mkdir rss-tracker && cd rss-tracker
curl -o docker-compose.yml https://raw.githubusercontent.com/jonthekiller/rss-tracker/main/docker-compose.yml
curl -o .env.example https://raw.githubusercontent.com/jonthekiller/rss-tracker/main/.env.example
cp .env.example .env
# Edit .env: set DISCORD_TOKEN (and adjust LOCAL_UID/LOCAL_GID to your user)

docker compose up -d
```

Or in a single command:

```bash
docker run -d --name rss-tracker --restart unless-stopped \
  -e DISCORD_TOKEN=xxx -v ./data:/app/data jonthekiller/rss-tracker:latest
```

Create the bot on the [Discord developer portal](https://discord.com/developers/applications):
1. **Bot** tab → *Reset Token* → copy it into `.env`
2. Invite: OAuth2 → URL Generator → scopes `bot` + `applications.commands`, *Manage Server* permission recommended for installation

Slash commands are registered globally at first startup (allow up to ~1 h for Discord propagation, immediate on servers where the bot is present via reconnection).

### From source

```bash
git clone https://github.com/jonthekiller/rss-tracker.git
cd rss-tracker
cp .env.example .env   # set DISCORD_TOKEN
docker compose up -d --build
```

## Discord commands

**Permission model (per channel)** — everything is managed from the channel itself:

- **Configuring a channel's feeds** (`/feed …`): you only need the **Send Messages** permission in that channel (server admins have full access). You cannot accidentally change another channel's config: commands targeting a different channel are refused.
- **`/settings`, `/import`, `/backup`**: also operate **per channel** (the current channel), with the same write-permission requirement. `/backup export all_channels:true` (whole server) remains restricted to **Manage Server**.
- **Read-only** (`/status`, `/debug`): open to everyone, as ephemeral messages.
- Id autocompletion only offers feeds from channels you can manage.

| Command | Description |
|---|---|
| `/feed add url [channel] [name]` | Add a feed (existing articles are marked as seen, nothing is sent) |
| `/feed list [channel]` | List feeds |
| `/feed show id` | Full feed configuration (including filter ids) |
| `/feed remove id` | Remove a feed |
| `/feed pause id` / `/feed resume id` | Pause / resume |
| `/feed move id channel` | Move to another channel |
| `/feed template id` | Edit the message template (modal) |
| `/feed embed id` | Edit the JSON embed template (modal, empty = none) |
| `/feed frequency id minutes` | Check frequency (min. 2 min) |
| `/feed batch id mode [threshold] [max_items]` | Burst mode: `auto` / `digest` / `individual` |
| `/feed filter add id kind field text [regex]` | Add a filter |
| `/feed filter remove id filter_id` | Remove a filter |
| `/feed test id [n] [mode]` | **Simulate a send**: `preview` (ephemeral, default) or `send` (real message in the current channel, state unchanged) |
| `/feed refresh id` | Check immediately |
| `/debug feed id` | **Dry run**: fetch + detailed report (seen/new/blocked articles, template rendering, errors) without changing anything |
| `/debug system` | Diagnostics: memory, send queues, scheduler, version |
| `/import monitorss file [channel]` | Import a MonitoRSS backup into this channel (backup channels require write permission, otherwise the feed is skipped) |
| `/backup export [channel] [all_channels]` | Export config — **current channel by default**; `all_channels:true` (Manage Server) exports everything |
| `/backup restore file` | Restore a backup (`.json` attachment) — each channel in the backup requires write permission |
| `/settings alert [user] [channel]` | Failure alerts **for this channel** (run the command in the channel to configure) |
| `/status` | Bot health: failing feeds, send queues, scheduler |

`id` supports autocompletion (type the feed name).

## Templates

Default text: `**{title}**\n{link}`. MonitoRSS-compatible example:

```
:newspaper:  |  **{title}**

{link}

{description}
```

Placeholders: `{title}`, `{link}`, `{description}` (HTML stripped), `{summary}` (truncated to 300 chars), `{author}`, `{date}`, `{image}`, `{feed.name}`. Content is truncated to Discord limits (2000 chars).

**JSON embed** (replaces the text when set):

```json
{
  "title": "{title}",
  "description": "{summary}",
  "color": "#5865F2",
  "timestamp": "article",
  "footerText": "{feed.name}",
  "fields": [{ "name": "Author", "value": "{author}" }]
}
```

`timestamp`: `"article"` (the article's date) or `"now"`.

## Bursts (batch)

When N new articles arrive in the same check cycle:

- `individual`: one message per article, in publication order, spaced by the send queue
- `digest`: always a numbered recap (beyond `max_items`: "…and X more")
- `auto` (default): digest if N ≥ `threshold` (default 5), otherwise individual

## Backup / restore

- `/backup export`: versioned JSON **v2 grouped by channel** (feeds + filters + channel settings + **seen article keys** → restore with no duplicates or replayed bursts). By default only exports the current channel; `all_channels:true` (Manage Server) exports everything.
- `/backup restore`: upsert by stable UUID; **every channel in the backup requires write permission** (otherwise the group is skipped); feeds whose channel no longer exists are imported paused (remap them with `/feed move`). v1 backups (old exports) are converted automatically (global v1 settings are ignored).
- Daily auto-backup in `data/backups/` (rotation: `BACKUP_RETENTION_DAYS` files kept, default 7 — hour via `BACKUP_HOUR`) — it always exports all channels.
- Optional CLI: `npm run backup` / `npm run restore -- file.json` / `npm run import -- backup.json --guild <id> --channel <id>`

## Per-channel settings

`/settings alert` attaches to the channel where the command is run: when one of that channel's feeds goes down (3 consecutive failures), the bot DMs the configured user and/or posts in the configured channel. Alerts are independent per channel. The MonitoRSS import automatically repositions `profile.alert` onto the import channel.

## MonitoRSS import (v6)

The expected format is the **MonitoRSS v6 backup**: the one produced by the `backup` command of MonitoRSS v6 (or the MonitoRSS-Web v6 export). Backups from older versions (v4/v5, e.g. Discord.RSS) are not guaranteed — update MonitoRSS or export from a v6 instance.

1. On your MonitoRSS v6 instance: `backup` command (or web export) → grab the JSON
2. In Discord: `/import monitorss file:<backup.json>` in the destination channel
3. Per-feed report: ✅ imported / ⚠️ / ❌

**What is kept**: `title`, `url`, `text` (template as-is), `embeds` (first embed), the `filters` and `rfilters` (regex) include filters, and `profile.alert` → configured as the import channel's failure alert.

**What is ignored**: `checkDates`, `ncomparisons`/`pcomparisons`, `regexOps`, `filteredFormats`, `subscribers`, supporters/patrons (no equivalent in RSS-Tracker).

Each imported feed is "baseline": articles already published at import time are marked as seen, **nothing will be re-sent** for what MonitoRSS already posted.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `DISCORD_TOKEN` | — | **Required** |
| `DB_PATH` | `./data/rss-tracker.db` | SQLite file |
| `DEFAULT_INTERVAL_MINUTES` | `10` | Default frequency for new feeds |
| `SEND_DELAY_MS` | `1000` | Delay between two messages to the same channel |
| `LOG_LEVEL` | `info` | trace/debug/info/warn/error |
| `BACKUP_HOUR` | `3` | Auto-backup hour (empty = disabled) |
| `BACKUP_RETENTION_DAYS` | `7` | Number of daily backup files kept in `data/backups/` |
| `TIMEZONE` | `UTC` | Timezone for `{date}` |
| `LOCAL_UID` / `LOCAL_GID` | `1000` | UID/GID of the process in the container (docker compose only — must match the owner of `./data`) |

## Troubleshooting

- **From Discord**: `/feed test id mode:send` posts the latest article with the current template (without marking it seen); `/debug feed id` does a full dry run (HTTP fetch, detected articles, filters, rendering, feed state, send queue); `/status` and `/debug system` for global health.
- **From the container**: logs are readable directly (`docker logs -f rss-tracker`). Every feed failure is logged with its name/URL/error, every delivery with the article count. Set `LOG_LEVEL=debug` for cycle details.

## Home Assistant add-on

The add-on installs from the dedicated add-on repository (prebuilt image, no build on the device):

1. **Settings → Add-ons → ⋮ → Repositories** → add `https://github.com/jonthekiller/ha-addons`
2. Install **RSS-Tracker** from the list
3. Paste your `discord_token` in the add-on configuration and start it

Data (SQLite + backups) lives in `/data` — persistent and included in Home Assistant backups. See [addon/README.md](addon/README.md) for details.

## Development

```bash
npm install
npm run dev          # tsx (no build)
npm test             # vitest
npm run lint         # biome
npm run build        # tsc → dist/
```

## Publishing a new version (maintainers)

1. Bump `version` in `package.json` **and** `addon/config.json` (identical)
2. Commit, then `git tag v1.0.x && git push origin main --tags`
3. The [.github/workflows/docker-publish.yml](.github/workflows/docker-publish.yml) workflow builds and publishes:
   - `jonthekiller/rss-tracker:<version>` + `:latest` (amd64 + arm64)
   - `jonthekiller/rss-tracker-addon-{amd64,aarch64}:<version>`
4. Required repository secrets: `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN` (never hardcode tokens in the code)

## Architecture

```
src/
  config.ts          Configuration (.env)
  db/                SQLite (WAL, migrations) + CRUD
  feed/              Fetcher (ETag/backoff) + new-article detection
  render/            Text/embed templates + digest
  scheduler/         Check loop + delivery pipeline
  sender/            Per-channel FIFO queue (pacing, 429)
  bot/               discord.js client + slash commands
  backup/            Export/restore/auto-backup
  import/            MonitoRSS import
  cli/               Optional CLI wrappers
```

Accepted trade-off (lite version): the send queue is in-memory — a crash may lose a few pending messages but **never duplicates** (keys are marked seen at enqueue).
