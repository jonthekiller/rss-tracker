# Home Assistant add-on — RSS-Tracker

The add-on ships **prebuilt**: no compilation needed on the device.

## Installation

1. In Home Assistant: **Settings → Add-ons → ⋮ → Repositories** → add:
   ```
   https://github.com/jonthekiller/ha-addons
   ```
2. **RSS-Tracker** appears in the list — install it.
3. In **Configuration**, paste your `discord_token` (other options are optional) — they are translated into environment variables at startup.
4. **Start**. Data (SQLite + backups) lives in `/data` — persistent and included in Home Assistant backups.

## Notes

- Slash commands are registered globally at bot startup.
- Logs: the add-on "Log" page, or `docker logs addon_local_rss_tracker`.
- The add-on needs no special network access beyond the Internet (Discord + RSS feeds): no `host_network`, no Home Assistant API.
- Image pulled from Docker Hub: `jonthekiller/rss-tracker-addon-{amd64,aarch64}:<version>` — built by GitHub Actions on every `v*` tag.

## Contents

| File | Role |
|---|---|
| `config.json` | Add-on manifest: options shown in the Home Assistant UI + prebuilt image reference |
| `icon.png` / `logo.png` | Icons shown by the Home Assistant add-on store (128×128 and 256×256) |
| `Dockerfile` | Builds the add-on image from the application image (`FROM jonthekiller/rss-tracker:<version>`) — used by CI |
| `run.sh` | Entrypoint: reads `/data/options.json`, exports env vars, starts the bot |
| `export-options.js` | Translates UI options into environment variables |

## Releasing a new version

The version in `config.json` must match the git tag (`v1.0.0` → `1.0.0`): CI enforces it and refuses to publish otherwise. To release a new add-on version, bump `version` in `config.json` **and** `package.json` at the same time.
