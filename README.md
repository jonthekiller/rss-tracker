# RRS-Tracker

Bot Discord léger auto-hébergé pour surveiller des flux RSS — une alternative minimaliste à MonitoRSS.

**Caractéristiques :**

- 🤖 Un seul bot pour X channels/serveurs — chaque feed a sa propre configuration
- 📰 Templates personnalisables (`{title}`, `{link}`, `{description}`, `{summary}`, `{author}`, `{date}`, `{image}`, `{feed.name}`) + embeds JSON optionnels
- 📚 Gestion des rafales : si un feed publie X articles d'un coup, un **digest** regroupé est envoyé au-delà d'un seuil (mode `auto`), ou toujours / jamais (`digest` / `individual`)
- 🔎 Filtres include/exclude par mot-clé ou regex (titre, description)
- ⏱️ Fréquence de vérification par feed (défaut 10 min), GET conditionnels (ETag/Last-Modified), backoff exponentiel en cas d'échec
- 🔔 Alertes d'échec : DM ou message quand un feed tombe en panne (3 échecs consécutifs)
- 💾 SQLite embarquée (aucun MongoDB/Redis), import **MonitoRSS** et backup/restore complets **depuis Discord**
- 🐳 Image Docker ~250 MB, ~100–150 MB RAM, CPU quasi nul hors vérifications

## Installation (Docker)

```bash
git clone git@jonthekiller.synology.me:jonthekiller/rss-tracker.git
cd rss-tracker
cp .env.example .env
# Éditez .env : renseignez DISCORD_TOKEN

docker compose up -d --build
```

Créez le bot sur le [portail développeur Discord](https://discord.com/developers/applications) :
1. Onglet **Bot** → *Reset Token* → copiez-le dans `.env`
2. Invitation : OAuth2 → URL Generator → scopes `bot` + `applications.commands`, permission *Manage Server* recommandée pour l'installation

Les commandes slash sont enregistrées globalement au premier démarrage (comptez jusqu'à ~1 h pour la propagation Discord, immédiat sur les serveurs où le bot est présent via reconnexion).

## Commandes Discord

Toutes les commandes nécessitent la permission **Manage Server**.

| Commande | Description |
|---|---|
| `/feed add url [channel] [name]` | Ajouter un flux (les articles existants sont marqués « vus », rien n'est envoyé) |
| `/feed list [channel]` | Lister les flux |
| `/feed show id` | Configuration complète d'un flux (avec les ids des filtres) |
| `/feed remove id` | Supprimer un flux |
| `/feed pause id` / `/feed resume id` | Mettre en pause / reprendre |
| `/feed move id channel` | Déplacer vers un autre channel |
| `/feed template id` | Éditer le template du message (modale) |
| `/feed embed id` | Éditer le template d'embed JSON (modale, vide = aucun) |
| `/feed frequency id minutes` | Fréquence de vérification (min. 2 min) |
| `/feed batch id mode [threshold] [max_items]` | Mode de rafale : `auto` / `digest` / `individual` |
| `/feed filter add id kind field text [regex]` | Ajouter un filtre |
| `/feed filter remove id filter_id` | Retirer un filtre |
| `/feed test id [n]` | Prévisualiser les n derniers articles (éphémère) |
| `/feed refresh id` | Vérifier immédiatement |
| `/import monitorss file [channel]` | Importer un backup MonitoRSS (pièce jointe `.json`) |
| `/backup export` | Exporter toute la configuration (JSON en pièce jointe) |
| `/backup restore file` | Restaurer un backup (pièce jointe `.json`) |
| `/settings alert [user] [channel]` | Destinataire des alertes d'échec |
| `/status` | Santé du bot : flux en échec, files d'envoi, scheduler |

`id` accepte l'autocomplétion (tapez le nom du flux).

## Templates

Texte par défaut : `**{title}**\n{link}`. Exemple MonitoRSS compatible :

```
:newspaper:  |  **{title}**

{link}

{description}
```

Placeholders : `{title}`, `{link}`, `{description}` (HTML strippé), `{summary}` (tronqué 300 c.), `{author}`, `{date}`, `{image}`, `{feed.name}`. Le contenu est tronqué aux limites Discord (2000 c.).

**Embed JSON** (remplace le texte si défini) :

```json
{
  "title": "{title}",
  "description": "{summary}",
  "color": "#5865F2",
  "timestamp": "article",
  "footerText": "{feed.name}",
  "fields": [{ "name": "Auteur", "value": "{author}" }]
}
```

`timestamp` : `"article"` (date de l'article) ou `"now"`.

## Rafales (batch)

Quand N nouveaux articles arrivent dans le même cycle de vérification :

- `individual` : un message par article, dans l'ordre de publication, espacés par la file d'envoi
- `digest` : toujours un récapitulatif numéroté (au-delà de `max_items` : « …and X more »)
- `auto` (défaut) : digest si N ≥ `threshold` (défaut 5), sinon individuel

## Backup / restauration

- `/backup export` : JSON versionné (feeds, filtres, settings, **clés des articles vus** → restauration sans doublon ni rafale rejouée)
- `/backup restore` : upsert par id stable ; les flux dont le channel n'existe plus sont importés **en pause** (remappez avec `/feed move`)
- Auto-backup quotidien dans `data/backups/` (rotation 7 jours, heure via `BACKUP_HOUR`)
- CLI optionnel : `npm run backup` / `npm run restore -- fichier.json` / `npm run import -- backup.json --guild <id> --channel <id>`

## Import MonitoRSS

1. Sur votre instance MonitoRSS : commandes `backup` (ou export web) → récupérez le JSON
2. Dans Discord : `/import monitorss file:<backup.json>`
3. Rapport par flux : ✅ importé / ⚠️ / ❌ — templates, filtres inclus (`filters` et `rfilters`) conservés ; chaque flux est « baseline » (rien ne sera renvoyé pour les articles déjà postés par MonitoRSS)

## Variables d'environnement

| Variable | Défaut | Description |
|---|---|---|
| `DISCORD_TOKEN` | — | **Requis** |
| `DB_PATH` | `./data/rrs-tracker.db` | Fichier SQLite |
| `DEFAULT_INTERVAL_MINUTES` | `10` | Fréquence par défaut des nouveaux flux |
| `SEND_DELAY_MS` | `1000` | Délai entre 2 messages sur un même channel |
| `LOG_LEVEL` | `info` | trace/debug/info/warn/error |
| `BACKUP_HOUR` | `3` | Heure de l'auto-backup (vide = désactivé) |
| `TIMEZONE` | `UTC` | Fuseau pour `{date}` |

## Développement

```bash
npm install
npm run dev          # tsx (pas de build)
npm test             # vitest
npm run lint         # biome
npm run build        # tsc → dist/
```

## Architecture

```
src/
  config.ts          Configuration (.env)
  db/                SQLite (WAL, migrations) + CRUD
  feed/              Fetcher (ETag/backoff) + détection de nouveautés
  render/            Templates texte/embed + digest
  scheduler/         Boucle de vérification + pipeline de livraison
  sender/            File FIFO par channel (pacing, 429)
  bot/               Client discord.js + commandes slash
  backup/            Export/restore/auto-backup
  import/            Import MonitoRSS
  cli/               Wrappers CLI optionnels
```

Compromis assumé (version lite) : la file d'envoi est en mémoire — un crash peut perdre quelques messages en attente mais **ne duplique jamais** (les clés sont marquées vues à l'enqueue).
