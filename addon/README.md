# Add-on Home Assistant — RSS-Tracker

L'add-on est distribué **pré-construit** : aucune compilation n'est nécessaire sur le device.

## Installation

1. Dans Home Assistant : **Paramètres → Modules complémentaires → ⋮ → Dépôts** → ajoutez :
   ```
   https://github.com/jonthekiller/rss-tracker
   ```
2. Le module **RSS-Tracker** apparaît — installez-le.
3. Dans **Configuration**, collez votre `discord_token` (les autres options sont optionnelles) — elles sont traduites en variables d'environnement au démarrage.
4. **Démarrer**. Les données (SQLite + backups) vivent dans `/data` — persistantes et incluses dans les sauvegardes Home Assistant.

## Notes

- Les commandes slash sont enregistrées globalement au démarrage du bot.
- Logs : page « Journal » de l'add-on, ou `docker logs addon_local_rss_tracker`.
- L'add-on n'a besoin d'aucun accès réseau particulier au-delà d'Internet (Discord + flux RSS) : pas de `host_network`, pas d'API Home Assistant.
- Image tirée de Docker Hub : `jonthekiller/rss-tracker-addon-{amd64,aarch64}:<version>` — construite par GitHub Actions à chaque tag `v*`.

## Contenu

| Fichier | Rôle |
|---|---|
| `config.json` | Manifeste add-on : options visibles dans l'UI Home Assistant + référence de l'image pré-construite |
| `Dockerfile` | Construit l'image add-on à partir de l'image application (`FROM jonthekiller/rss-tracker:<version>`) — utilisé par la CI |
| `run.sh` | Point d'entrée : lit `/data/options.json`, exporte les variables d'env, lance le bot |
| `export-options.js` | Traduit les options de l'UI en variables d'environnement |

## Mise à jour d'une release

Le numéro de version de `config.json` doit correspondre au tag git (`v0.3.0` → `0.3.0`) : la CI le vérifie et refuse de publier sinon. Pour publier une nouvelle version de l'add-on, bump `version` dans `config.json` **et** `package.json` en même temps.
