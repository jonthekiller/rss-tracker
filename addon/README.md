# RSS-Tracker

[![Docker Hub](https://img.shields.io/docker/v/jonthekiller/rss-tracker?sort=semver&label=docker)](https://hub.docker.com/r/jonthekiller/rss-tracker)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/jonthekiller/rss-tracker/blob/main/LICENSE)

A lightweight self-hosted Discord bot for watching RSS feeds — a minimalist alternative to MonitoRSS.

> Source code and standalone Docker instructions: [jonthekiller/rss-tracker](https://github.com/jonthekiller/rss-tracker)

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

## Installation (Home Assistant add-on)

The add-on ships **prebuilt**: no compilation needed on the device.

1. In Home Assistant: **Settings → Add-ons → ⋮ → Repositories** → add:
   ```
   https://github.com/jonthekiller/ha-addons
   ```
2. **RSS-Tracker** appears in the list — install it.
3. In **Configuration**, paste your `discord_token` (other options are optional).
4. **Start**. Data (SQLite + backups) lives in `/data` — persistent and included in Home Assistant backups.

Create the bot on the [Discord developer portal](https://discord.com/developers/applications):
1. **Bot** tab → *Reset Token* → copy it into the add-on configuration
2. Invite: OAuth2 → URL Generator → scopes `bot` + `applications.commands`, *Manage Server* permission recommended for installation

Slash commands are registered globally at bot startup.

## Add-on configuration

| Option | Default | Description |
|---|---|---|
| `discord_token` | — | **Required** — Discord bot token |
| `default_interval_minutes` | `10` | Default check frequency for new feeds (2–1440) |
| `send_delay_ms` | `1000` | Delay between two messages to the same channel (250–10000) |
| `log_level` | `info` | trace / debug / info / warn / error |
| `backup_hour` | `3` | Daily auto-backup hour (0–23) |
| `timezone` | `UTC` | Timezone used for `{date}` |

Notes:

- Slash commands are registered globally at bot startup.
- Logs: the add-on "Log" page, or `docker logs addon_local_rss_tracker`.
- The add-on needs no special network access beyond the Internet (Discord + RSS feeds): no `host_network`, no Home Assistant API.
- Image pulled from Docker Hub: `jonthekiller/rss-tracker-addon-{amd64,aarch64}:<version>` — built by GitHub Actions on every `v*` tag.

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
| `/import monitorss file [channel]` | Import a MonitoRSS backup into this channel |
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
- `/backup restore`: upsert by stable UUID; **every channel in the backup requires write permission** (otherwise the group is skipped); feeds whose channel no longer exists are imported paused (remap them with `/feed move`). v1 backups (old exports) are converted automatically.
- Daily auto-backup in `/data/backups/` (7-day rotation, hour via `backup_hour`) — it always exports all channels.

## MonitoRSS import (v6)

The expected format is the **MonitoRSS v6 backup**: the one produced by the `backup` command of MonitoRSS v6 (or the MonitoRSS-Web v6 export). Backups from older versions (v4/v5, e.g. Discord.RSS) are not guaranteed — update MonitoRSS or export from a v6 instance.

1. On your MonitoRSS v6 instance: `backup` command (or web export) → grab the JSON
2. In Discord: `/import monitorss file:<backup.json>` in the destination channel
3. Per-feed report: ✅ imported / ⚠️ / ❌

**What is kept**: `title`, `url`, `text` (template as-is), `embeds` (first embed), the `filters` and `rfilters` (regex) include filters, and `profile.alert` → configured as the import channel's failure alert.

**What is ignored**: `checkDates`, `ncomparisons`/`pcomparisons`, `regexOps`, `filteredFormats`, `subscribers`, supporters/patrons (no equivalent in RSS-Tracker).

Each imported feed is "baseline": articles already published at import time are marked as seen, **nothing will be re-sent** for what MonitoRSS already posted.

## Troubleshooting

- **From Discord**: `/feed test id mode:send` posts the latest article with the current template (without marking it seen); `/debug feed id` does a full dry run (HTTP fetch, detected articles, filters, rendering, feed state, send queue); `/status` and `/debug system` for global health.
- **From Home Assistant**: the add-on "Log" page shows every feed failure (name/URL/error) and every delivery. Set `log_level` to `debug` for cycle details.
