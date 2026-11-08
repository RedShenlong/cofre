#!/usr/bin/env bash
# Desinstala Cofre de macOS. Con --purge borra también los datos cifrados.
set -euo pipefail
LABEL="com.redshenlong.cofre"
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
rm -f "$HOME/Library/LaunchAgents/$LABEL.plist" "$HOME/.local/bin/cofre"
if [ "${1:-}" = "--purge" ]; then
  rm -rf "$HOME/Library/Application Support/Cofre" "$HOME/Library/Logs/Cofre"
  echo "✓ Cofre desinstalado y datos borrados."
else
  echo "✓ Cofre desinstalado. Tus datos siguen en ~/Library/Application Support/Cofre (bórralos con: $0 --purge)."
fi
