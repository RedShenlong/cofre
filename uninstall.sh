#!/usr/bin/env bash
# Desinstala Cofre. Con --purge borra también los datos cifrados.
set -euo pipefail
systemctl --user disable --now cofre.service 2>/dev/null || true
rm -f ~/.config/systemd/user/cofre.service ~/.local/bin/cofre
systemctl --user daemon-reload
if [ "${1:-}" = "--purge" ]; then
  rm -rf ~/.local/share/cofre
  echo "✓ Cofre desinstalado y datos borrados."
else
  echo "✓ Cofre desinstalado. Tus datos siguen en ~/.local/share/cofre (bórralos con: $0 --purge)."
fi
