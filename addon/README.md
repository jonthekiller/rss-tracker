# Add-on Home Assistant — RRS-Tracker

Ce dossier transforme RRS-Tracker en [add-on Home Assistant](https://developers.home-assistant.io/docs/add-ons/) (utile si vous hébergez déjà Home Assistant et ne voulez pas d'un conteneur séparé).

## Contenu

| Fichier | Rôle |
|---|---|
| `config.json` | Manifeste add-on (options visibles dans l'UI Home Assistant) |
| `Dockerfile` | Build multi-stage — **attend `app/` avec les sources** |
| `run.sh` | Point d'entrée : lit `/data/options.json`, exporte les variables d'env, lance le bot |
| `export-options.js` | Traduit les options de l'UI en variables d'environnement |

## Installation

1. **Préparer les sources** (à refaire après chaque mise à jour du code) :
   ```bash
   ./scripts/prepare-addon.sh
   ```

2. **Copier le dossier `addon/`** sur la machine Home Assistant, par exemple :
   ```bash
   scp -r addon/ hass@homeassistant:/usr/share/hassio/addons/local/rrs-tracker
   ```
   (sur HAOS, utilisez le Samba share `addons` ou l'add-on « File editor »)

3. Dans Home Assistant : **Paramètres → Modules complémentaires → ⋮ → Dépôts** → ajouter `/usr/share/hassio/addons/local` (si ce n'est pas déjà fait), puis le module « RRS-Tracker » apparaît.

4. **Configuration** : collez votre `discord_token`, ajustez les autres options (toutes optionnelles sauf le token). Le token et les autres réglages sont traduits en variables d'environnement au démarrage.

5. **Démarrer**. Les données (SQLite + backups) vivent dans `/data` — persistant et sauvegardé par Home Assistant.

## Notes

- Les commandes slash sont enregistrées globalement au démarrage du bot.
- Logs : page « Journal » de l'add-on (sortie lisible via pino-pretty), ou `docker logs addon_local_rrs_tracker`.
- L'add-on n'a besoin d'aucun accès réseau particulier au-delà d'Internet (Discord + flux RSS) : pas de `host_network`, pas d'API Home Assistant.
