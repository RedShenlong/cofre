#!/usr/bin/env bash
# Compila e instala Cofre como servicio de usuario de systemd (no necesita sudo).
set -euo pipefail
cd "$(dirname "$0")"

bold=$'\e[1m'; green=$'\e[32m'; dim=$'\e[2m'; reset=$'\e[0m'
step() { printf '%s→%s %s\n' "$bold" "$reset" "$1"; }

GO=$(command -v go || true)
[ -z "$GO" ] && [ -x "$HOME/.local/go/bin/go" ] && GO="$HOME/.local/go/bin/go"
if [ -z "$GO" ]; then
  echo "No encuentro Go (≥ 1.26). Instálalo, por ejemplo: sudo pacman -S go  ·  sudo apt install golang" >&2
  exit 1
fi

step "Compilando…"
"$GO" build -trimpath -ldflags="-s -w" -o cofre .

step "Instalando en ~/.local/bin/cofre"
mkdir -p ~/.local/bin ~/.local/share/cofre ~/.config/systemd/user
chmod 700 ~/.local/share/cofre
install -m 755 cofre ~/.local/bin/cofre
install -m 644 cofre.service ~/.config/systemd/user/cofre.service

step "Activando el servicio (arrancará solo al iniciar sesión)"
# Para que Cofre pueda usar el portapapeles y las notificaciones del escritorio.
systemctl --user import-environment WAYLAND_DISPLAY DISPLAY 2>/dev/null || true
systemctl --user daemon-reload
systemctl --user enable cofre.service >/dev/null 2>&1
systemctl --user restart cofre.service
sleep 1

if ! systemctl --user is-active --quiet cofre.service; then
  echo "El servicio no ha arrancado. Revisa: journalctl --user -u cofre -n 30" >&2
  exit 1
fi

HOST="$(hostname | cut -d. -f1 | tr '[:upper:]' '[:lower:]').local"
IFACE=$(ip route show default 2>/dev/null | awk '{print $5; exit}')
NET=$(ip route show dev "${IFACE:-x}" scope link proto kernel 2>/dev/null | awk '{print $1; exit}')

cat <<MSG

${green}${bold}✓ Cofre instalado y en marcha${reset} → https://${HOST}:8443

  ${dim}# siguientes pasos${reset}
  cofre ca                       certificado para tus dispositivos
  cofre codigo                   código para registrar tu llave
  cofre enviar archivo.pdf       enviar algo
  journalctl --user -u cofre -f  ver el log

MSG

if command -v ufw >/dev/null && [ -n "${NET:-}" ]; then
  cat <<MSG
  ${bold}Firewall (ufw):${reset} abre Cofre solo para tu red local (${NET} en ${IFACE}):
    sudo ufw allow in on ${IFACE} from ${NET} to any port 8443 proto tcp
    sudo ufw allow in on ${IFACE} from ${NET} to any port 5353 proto udp

MSG
fi
