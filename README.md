<h1 align="center">
  <img src="web/icon.svg" width="88" alt=""><br>
  Cofre
</h1>

<p align="center">
  Tu propio "AirDrop" para la red de casa: comparte archivos, fotos y textos entre
  Linux, Android e iPhone, <b>protegido con una llave de seguridad FIDO2</b> y cifrado en la red y en el disco.
</p>

<p align="center">
  <a href="https://github.com/RedShenlong/cofre/actions/workflows/ci.yml"><img src="https://github.com/RedShenlong/cofre/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/auth-FIDO2%20%2F%20WebAuthn-7c3aed" alt="FIDO2 / WebAuthn">
  <img src="https://img.shields.io/badge/TLS-1.3-16a34a" alt="TLS 1.3">
  <a href="LICENSE"><img src="https://img.shields.io/badge/licencia-MIT-blue" alt="MIT"></a>
</p>

<p align="center">
  <img src="docs/screenshots/mobile-light.png" width="260" alt="Cofre en el móvil (tema claro)">
  &nbsp;
  <img src="docs/screenshots/mobile-dark.png" width="260" alt="Cofre en el móvil (tema oscuro)">
  &nbsp;
  <img src="docs/screenshots/settings.png" width="260" alt="Ajustes: dispositivos y llaves">
</p>

---

## ¿Por qué?

Herramientas como LocalSend envían archivos **de un dispositivo a otro**, de uno en uno.
Si generas un PDF en el portátil y lo quieres en la tablet **y** en el móvil, toca enviarlo
dos veces o acabar mandándotelo por WhatsApp.

**Cofre** cambia el enfoque: el portátil hace de **cofre compartido** en tu red local.
Subes algo una vez, desde la terminal o desde cualquier navegador, y aparece **al instante en
todos tus dispositivos**. Para entrar hace falta tu llave física (YubiKey, Google Titan…), así
que nadie más de tu wifi puede ver nada.

## Características

- ⚡ **Tiempo real**: lo que subes aparece al momento en todos los dispositivos conectados (Server-Sent Events).
- 🔑 **Login con llave FIDO2/WebAuthn**: sin contraseñas, por NFC o USB-C. Admite varias llaves (titular y repuesto).
- 🔒 **Cifrado en todas partes**: TLS 1.3 en la red y AES-256-GCM en el disco, incluidos los nombres de archivo.
- 📋 **Portapapeles compartido**: lo que envías desde el móvil se copia solo en el portátil (Wayland) y, en los móviles, queda a un toque.
- 🖥️ **CLI** con barra de progreso: `cofre enviar apuntes.pdf`.
- 📱 **App instalable (PWA)**: se añade a la pantalla de inicio del iPhone y de Android como una app más.
- 🖱️ **Arrastrar y soltar**, pegar capturas con <kbd>Ctrl</kbd>+<kbd>V</kbd>, textos con <kbd>Ctrl</kbd>+<kbd>Enter</kbd>.
- 📌 **Autolimpieza**: lo antiguo se borra solo (configurable), salvo lo que fijes.
- 👥 **Gestión de dispositivos**: ve qué dispositivos tienen sesión y expulsa cualquiera al instante.
- 🪶 **Ligero**: un único binario en Go sin dependencias externas. En reposo usa unos 20 MB de RAM y prácticamente nada de CPU.

<p align="center">
  <img src="docs/screenshots/terminal.png" width="760" alt="cofre enviar con barra de progreso">
</p>

## Cómo funciona

```mermaid
flowchart LR
    subgraph Portátil["💻 Portátil (Linux)"]
        CLI["cofre enviar"] -- socket Unix 0600 --> S
        S["cofre serve<br/>(servicio systemd)"] --> D[("~/.local/share/cofre<br/>AES-256-GCM")]
        S -. wl-copy .-> CB["Portapapeles"]
    end
    T["📲 Tablet Android"] <-- "HTTPS · TLS 1.3<br/>WebAuthn + SSE" --> S
    I["📱 iPhone"] <-- "HTTPS · TLS 1.3<br/>WebAuthn + SSE" --> S
    K(["🔑 Llave FIDO2"]) -. NFC / USB-C .- T
    K -. NFC / USB-C .- I
```

- El portátil ejecuta `cofre serve` como **servicio de usuario** de systemd. Sirve la web por HTTPS en el puerto `8443`.
- Los demás dispositivos lo encuentran por **mDNS** en `https://<nombre-del-portátil>.local:8443`. No hace falta saber la IP.
- La CLI habla con el servidor por un **socket Unix** privado, así que no necesita llave.

## Requisitos

| | |
|---|---|
| **Servidor** | Linux con systemd y Avahi (mDNS). Se ha probado en Arch / CachyOS con Hyprland. |
| **Go** | 1.26 o superior, solo para compilar. |
| **Llave** | Cualquier llave FIDO2: Google Titan, YubiKey, SoloKey, Nitrokey… |
| **Clientes** | Android con Chrome · iPhone/iPad con Safari · cualquier navegador moderno en escritorio. |
| **Red** | Todos los dispositivos en la misma red local. |
| *Opcional* | `wl-clipboard` y `notify-send` para el portapapeles compartido en Wayland. |

## Instalación

### 1. Compilar e instalar el servicio

```bash
git clone https://github.com/RedShenlong/cofre.git
cd cofre
./install.sh        # o: make install
```

El instalador hace esto:
- Compila el binario y lo copia en `~/.local/bin/cofre`.
- Instala y arranca el servicio de usuario `cofre.service`, que arrancará solo cada vez que inicies sesión.
- Muestra tu dirección (`https://<host>.local:8443`) y las reglas de firewall que necesitas.

No necesita `sudo`.

> **Avahi** debe estar activo para que funcione `<host>.local`:
> `sudo systemctl enable --now avahi-daemon`

### 2. Abrir el firewall (solo si usas uno)

Con `ufw`, permite el acceso **solo desde tu red local**. El instalador te imprime estas
líneas ya rellenadas con tu interfaz y tu subred:

```bash
sudo ufw allow in on wlan0 from 192.168.1.0/24 to any port 8443 proto tcp   # Cofre
sudo ufw allow in on wlan0 from 192.168.1.0/24 to any port 5353 proto udp   # mDNS
```

### 3. Instalar el certificado en cada dispositivo

Cofre genera **su propia autoridad de certificación (CA)**, que solo es válida para tu
portátil. Se instala **una vez** en cada dispositivo. Para ver la huella y las rutas:

```bash
cofre ca
```

<details>
<summary><b>📲 Android (probado en Samsung Galaxy Tab)</b></summary>

1. Envía `~/.local/share/cofre/tls/cofre-ca.crt` a la tablet con **LocalSend**, que va cifrado. También puedes descargarlo en Chrome desde `https://<host>.local:8443/ca/cofre-ca.crt`.
2. **Ajustes → Seguridad y privacidad → Más ajustes de seguridad → Instalar desde almacenamiento del dispositivo → Certificado de CA**.
3. Toca **Instalar de todos modos**, desbloquea con tu PIN o patrón y elige `cofre-ca.crt`.
4. Comprueba la huella en **Ver certificados de seguridad → Usuario → Cofre CA**. Tiene que coincidir con la que muestra `cofre ca`.

Usa **Chrome**. Samsung Internet no gestiona bien las llaves de seguridad.
</details>

<details>
<summary><b>📱 iPhone / iPad</b></summary>

1. Abre **Safari** (tiene que ser Safari) y ve a `https://<host>.local:8443/ca/cofre-ca.crt`.
2. Saldrá el aviso "Esta conexión no es privada", que es normal la primera vez. Toca **Mostrar detalles → visitar este sitio web**.
3. Cuando pregunte si quieres permitir la descarga del perfil, toca **Permitir**.
4. **Ajustes → Perfil descargado → Instalar** e introduce el código del iPhone.
5. ⚠️ **No te saltes este paso:** **Ajustes → General → Información → Ajustes de confianza de certificados** y activa **Cofre CA**.
6. Comprueba la huella en **Ajustes → General → VPN y gestión de dispositivos → Cofre CA → Más detalles → SHA-256**.
</details>

<details>
<summary><b>💻 Navegador del propio portátil (Chrome / Chromium / Brave)</b></summary>

```bash
certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Cofre CA" -i ~/.local/share/cofre/tls/cofre-ca.crt
```

Firefox usa su propio almacén: **Ajustes → Privacidad y seguridad → Certificados → Ver certificados → Autoridades → Importar**.
</details>

**¿Está bien instalado?** Abre `https://<host>.local:8443`. Si la página carga **sin ningún aviso**,
el certificado está bien.

### 4. Registrar tu llave (desde el portátil)

Por seguridad, la llave **solo se puede registrar desde el propio portátil**:

```bash
cofre registrar
```

Se abre el navegador del portátil. Ponle nombre a la llave, pulsa **Registrar llave** y tócala.

La llave se registra **una vez** y sirve en **todos** tus dispositivos. En el móvil y en la tablet
solo tienes que tocar **"Entrar con mi llave"**.

> Cofre **no guarda nada dentro de la llave**: no ocupa espacio de passkeys y no toca las que
> ya tengas. Te recomiendo registrar también una **llave de repuesto** desde ⚙️ Ajustes.

### 5. Añadirlo a la pantalla de inicio

- **iPhone (Safari):** Compartir → **Añadir a pantalla de inicio**. La app de inicio no comparte sesión con Safari, así que la primera vez tendrás que entrar con la llave otra vez.
- **Android (Chrome):** ⋮ → **Añadir a pantalla de inicio → Instalar**.

<p align="center">
  <img src="docs/screenshots/desktop.png" width="820" alt="Cofre en el escritorio">
</p>

## Uso

### Desde la terminal del portátil

```bash
cofre enviar apuntes.pdf soluciones.pdf      # uno o varios archivos, con barra de progreso
cofre enviar -t "nos vemos a las 5"           # un texto
git diff | cofre enviar -                     # lo que llegue por la entrada estándar
cofre estado                                  # comprobar que el servidor funciona
```

### Desde cualquier navegador

- **Archivos:** arrástralos a la ventana, usa el botón **Archivos** o pega una captura con <kbd>Ctrl</kbd>+<kbd>V</kbd>.
- **Textos:** escríbelos y envíalos con **Enviar** o <kbd>Ctrl</kbd>+<kbd>Enter</kbd>.
- **Acciones de cada elemento:** **Abrir**, **Guardar**, **Copiar** (textos e imágenes), **Fijar** (📌, no se borra solo) y **Borrar**.

### Portapapeles compartido

| Dispositivo | Qué pasa cuando llega un texto o una imagen desde otro dispositivo |
|---|---|
| 💻 Portátil (Wayland) | Se copia **automáticamente**, con notificación, aunque no tengas la web abierta. |
| 📲 Android | Se copia solo si tienes Cofre abierto en pantalla. Si no, aparece un aviso para copiarlo con un toque. |
| 📱 iPhone | iOS exige un toque: aparece un aviso **"Copiar"** al abrir la app. |

Se activa y desactiva en ⚙️ Ajustes → Preferencias.

### Comandos

| Comando | Descripción |
|---|---|
| `cofre serve [opciones]` | Arranca el servidor. Normalmente lo lanza systemd. |
| `cofre enviar ARCHIVO…` / `-t "texto"` / `-` | Sube archivos o texto. |
| `cofre registrar` | Registra una llave desde el portátil (abre el navegador). |
| `cofre reiniciar-llaves` | Borra todas las llaves y sesiones, por si pierdes tu llave. Tus archivos se conservan. |
| `cofre ca` | Muestra el certificado, su huella y cómo instalarlo. |
| `cofre estado` | Comprueba que el servidor está en marcha. |

**Opciones de `cofre serve`:**

| Opción | Por defecto | Descripción |
|---|---|---|
| `-host` | `<hostname>.local` | Nombre con el que te encuentran los dispositivos. |
| `-port` | `8443` | Puerto HTTPS. |
| `-addr` | *(todas)* | IP en la que escuchar. |
| `-data` | `~/.local/share/cofre` | Carpeta de datos (cifrados). |
| `-max-mb` | `16384` | Tamaño máximo de subida **desde el navegador**. La CLI no tiene límite. |

Para cambiarlas, edita `ExecStart` con `systemctl --user edit --full cofre`.

## Seguridad

Cofre está pensado para que **alguien conectado a tu misma wifi no pueda ver ni tocar nada**.

### En la red

| Amenaza | Protección |
|---|---|
| Espiar el tráfico | Todo va por **TLS 1.3**. No hay HTTP ni versiones antiguas de TLS. |
| Suplantar al servidor (MITM) | Tus dispositivos solo confían en tu CA. Su clave privada **se genera en memoria y se destruye** nada más firmar el certificado, así que no existe en ningún disco. |
| Usar tu CA para espiar otras webs | La CA lleva *Name Constraints* **críticas**: solo puede firmar certificados para `<host>.local`, y se excluyen todas las IPs. |
| Entrar sin permiso | **WebAuthn/FIDO2**. El reto es aleatorio y de un solo uso, va ligado al origen exacto, se detectan llaves clonadas y se pide el PIN de la llave si lo tiene. Solo se aceptan llaves físicas (`cross-platform`). |
| Robar la sesión | Cookie `__Host-`, `Secure`, `HttpOnly` y `SameSite=Strict`. En el servidor solo se guarda su hash SHA-256. Las sesiones caducan y se pueden revocar. |
| CSRF / DNS rebinding | Se comprueban las cabeceras `Origin` y `Host` en cada petición. |
| XSS / contenido malicioso | La página tiene una CSP estricta (`script-src 'self'`). Los archivos subidos se sirven en *sandbox* o como descarga, con `nosniff`. |
| Registrar una llave ajena | Las llaves solo se registran **desde el propio portátil**. `cofre registrar` genera un secreto de **256 bits**, de un solo uso y que caduca en 10 minutos. Viaja en el fragmento de la URL (`#`), así que nunca se envía en la dirección ni aparece en el historial. Además, el servidor rechaza cualquier registro que no venga de una IP del propio portátil. Con sesión iniciada se pueden añadir llaves de repuesto desde Ajustes. |

### En el disco

- Archivos, textos, **nombres de archivo**, llaves registradas y sesiones se guardan cifrados con **AES-256-GCM**.
- Cada archivo usa su propia clave, derivada con HKDF-SHA256.
- Los archivos se cifran en **trozos autenticados de 64 KiB**. Si alguien modifica, reordena o recorta un trozo, se detecta. Además así se puede buscar dentro de un archivo (*Range*) sin descifrarlo entero.
- La clave maestra está en `master.key`, con permisos `0600`. Protege los datos si alguien copia la carpeta sin esa clave. Para protegerte también del robo del portátil, usa además **cifrado de disco completo (LUKS)**.

### En el sistema

- El servicio de systemd está aislado (`ProtectSystem=strict`, `NoNewPrivileges`, `MemoryDenyWriteExecute`…). Solo puede escribir en su carpeta de datos y en su socket.
- La CLI usa un socket Unix con permisos `0600` dentro de `$XDG_RUNTIME_DIR`, así que solo tu usuario puede usarla.
- Los enlaces de abrir y descargar llevan una firma HMAC y caducan a los 15 minutos.

¿Has encontrado un fallo de seguridad? Mira [SECURITY.md](SECURITY.md).

## Solución de problemas

<details>
<summary><b>"Esta conexión no es privada" en un dispositivo</b></summary>

- **iPhone:** casi siempre falta activar la confianza en **Ajustes → General → Información → Ajustes de confianza de certificados**.
- **Android:** comprueba que lo instalaste como **Certificado de CA**, no como certificado de usuario ni de VPN.
- Abre siempre `https://<host>.local:8443` y **nunca la IP**. El certificado y las llaves van ligados al nombre.
</details>

<details>
<summary><b>La página no carga en el móvil o la tablet</b></summary>

- Comprueba que el dispositivo está en la misma wifi y que `systemctl --user status cofre` está activo.
- Revisa el firewall: hay que abrir el puerto `8443/tcp` y también `5353/udp` (mDNS).
- Comprueba que Avahi publica el nombre: `avahi-resolve -n <host>.local`.
</details>

<details>
<summary><b>"Las llaves solo se pueden registrar desde el propio portátil" / "Enlace no válido"</b></summary>

Ejecuta `cofre registrar` **en el portátil** y usa el navegador que se abre ahí. El enlace vale
una sola vez y caduca a los 10 minutos. Si se ha caducado, vuelve a ejecutar el comando.
</details>

<details>
<summary><b>He perdido mi llave</b></summary>

Si tienes una llave de repuesto registrada, entra con ella y borra la perdida en ⚙️ Ajustes.
Si no tienes ninguna, ejecuta en el portátil:
```bash
cofre reiniciar-llaves   # borra todas las llaves y sesiones; tus archivos se conservan
cofre registrar          # registra la nueva
```
</details>

<details>
<summary><b>"La llave no es válida"</b></summary>

Mira el motivo exacto en `journalctl --user -u cofre -n 20`. Las llaves registradas para un
nombre (`<host>.local`) solo valen para ese nombre. Si cambias el hostname del portátil, tendrás
que volver a registrarlas.
</details>

<details>
<summary><b>No se copia nada en el portapapeles del portátil</b></summary>

El servicio necesita las variables de tu sesión gráfica:
```bash
systemctl --user import-environment WAYLAND_DISPLAY
systemctl --user restart cofre
```
También necesitas `wl-clipboard` instalado.
</details>

## Desarrollo

```bash
make test     # pruebas (con detector de carreras)
make vet      # go vet + gofmt
make build    # compila ./cofre
```

Las pruebas incluyen un **flujo completo con una llave FIDO2 simulada por software**: alta, login,
subida y descarga. También comprueban que se rechazan las llaves falsas, la reutilización de un
reto, las sesiones revocadas, los archivos manipulados y los registros desde otra IP de la red.

```
.
├── main.go          CLI y arranque del servidor
├── server.go        HTTP, API, WebAuthn y tiempo real (SSE)
├── store.go         Estado y almacenamiento cifrado
├── crypto.go        AES-256-GCM por trozos y HKDF
├── tlsca.go         CA propia con Name Constraints
├── clipboard.go     Portapapeles de Wayland
├── progress.go      Barra de progreso de la CLI
├── icons.go         Iconos PNG de la PWA
├── web/             Interfaz (HTML + CSS + JS sin dependencias)
├── cofre.service    Unidad de systemd (aislada)
└── install.sh       Instalador
```

### Desinstalar

```bash
./uninstall.sh          # quita el servicio y el binario (los datos se conservan)
./uninstall.sh --purge  # también borra los datos cifrados
```

## Licencia

[MIT](LICENSE) © 2026 RedShenlong
