#!/usr/bin/env bash
# Compila e instala Cofre en macOS como agente de launchd (arranca solo al iniciar sesión).
set -euo pipefail
cd "$(dirname "$0")"

bold=$'\e[1m'; green=$'\e[32m'; dim=$'\e[2m'; reset=$'\e[0m'
step() { printf '%s→%s %s\n' "$bold" "$reset" "$1"; }

LABEL="com.redshenlong.cofre"
BIN_DIR="$HOME/.local/bin"
DATA="$HOME/Library/Application Support/Cofre"
LOG="$HOME/Library/Logs/Cofre/cofre.log"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"

if ! command -v go >/dev/null; then
  echo "No encuentro Go (≥ 1.26). Instálalo con: brew install go" >&2
  exit 1
fi

step "Compilando…"
go build -trimpath -ldflags="-s -w" -o cofre .

step "Instalando en $BIN_DIR/cofre"
mkdir -p "$BIN_DIR" "$DATA" "$(dirname "$LOG")" "$(dirname "$PLIST")"
chmod 700 "$DATA"
install -m 755 cofre "$BIN_DIR/cofre"
rm -f cofre

step "Creando el agente de launchd"
cat > "$PLIST" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$BIN_DIR/cofre</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>10</integer>
  <key>Nice</key><integer>10</integer>
  <key>StandardOutPath</key><string>$LOG</string>
  <key>StandardErrorPath</key><string>$LOG</string>
</dict>
</plist>
PL

step "Arrancando el servicio"
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
sleep 2

if ! launchctl print "gui/$(id -u)/$LABEL" 2>/dev/null | grep -q "state = running"; then
  echo "El servicio no ha arrancado. Revisa el log: tail -n 30 \"$LOG\"" >&2
  exit 1
fi

HOST="$(scutil --get LocalHostName 2>/dev/null | tr '[:upper:]' '[:lower:]').local"

cat <<MSG

${green}${bold}✓ Cofre instalado y en marcha${reset} → https://${HOST}:8443

MSG

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) printf '  %sAñade ~/.local/bin a tu PATH%s (una vez):\n    echo '"'"'export PATH="$HOME/.local/bin:$PATH"'"'"' >> ~/.zprofile && source ~/.zprofile\n\n' "$bold" "$reset" ;;
esac

cat <<MSG
  ${dim}# siguientes pasos${reset}
  cofre ca                       certificado para tus dispositivos
  cofre registrar                registrar tu llave (desde este Mac)
  cofre enviar archivo.pdf       enviar algo
  tail -f "$LOG"   ver el log

  ${bold}Firewall de macOS:${reset} si está activado, la primera vez macOS preguntará si
  "cofre" puede aceptar conexiones entrantes: pulsa ${bold}Permitir${reset}.

MSG
