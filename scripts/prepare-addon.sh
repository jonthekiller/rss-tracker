#!/bin/sh
# Prepares the Home Assistant add-on directory (addon/) by copying the
# application source into addon/app/. The add-on Dockerfile builds from there.
#
# Usage: ./scripts/prepare-addon.sh
# Then copy the whole addon/ folder to the Host Assistant "local addons"
# folder, e.g. /usr/share/hassio/addons/local/rrs-tracker, and add the
# repository in Settings → Add-ons → ⋮ → Repositories (or use the local path).
set -e
cd "$(dirname "$0")/.."

rm -rf addon/app
mkdir -p addon/app
cp package.json package-lock.json tsconfig.json addon/app/
cp -r src addon/app/src

echo "addon/app is ready. Copy the addon/ folder to your Home Assistant local add-ons directory."
