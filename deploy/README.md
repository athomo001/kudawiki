# Instalar Kudawiki con Docker

Kudawiki corre en **un solo contenedor**, con la base de datos y la interfaz adentro. Cada versión
publica una imagen firmada en `ghcr.io/athomo001/kudawiki` para Linux amd64 y arm64, así que no
hace falta clonar el repositorio ni compilar: bastan los dos archivos de esta carpeta,
`compose.yaml` y `.env.example`.

**Se necesita:** Linux con Docker Engine y Compose v2 (`docker compose version`), un nombre con
TLS para entrar desde la red (paso 5) y la hora del servidor sincronizada por NTP.

## 1. Bajar los archivos

Elegir la versión en [Releases](https://github.com/athomo001/kudawiki/releases) y bajar los
archivos de esa misma etiqueta, para que el `compose.yaml` corresponda a la imagen:

```sh
sudo mkdir -p /opt/kudawiki && sudo chown "$USER" /opt/kudawiki && cd /opt/kudawiki
v=1.0.0
curl -fsSLO "https://raw.githubusercontent.com/athomo001/kudawiki/v$v/deploy/compose.yaml"
curl -fsSL "https://raw.githubusercontent.com/athomo001/kudawiki/v$v/deploy/.env.example" -o .env
chmod 600 .env
```

## 2. Configurar `.env`

| Variable | Qué poner |
| :--- | :--- |
| `KUDAWIKI_VERSION` | `1` toma la última 1.x en cada `pull`; `1.0.0` deja la versión fija hasta que se cambie aquí ([versiones](#versiones)). |
| `KUDAWIKI_BASE_URL` | La dirección con que se entra, con `https://` (`https://wiki.empresa.cl`). Fuera de `localhost`, con `http://` la wiki no arranca. |
| `KUDAWIKI_TRUST_PROXY` | `true` con un proxy inverso delante (paso 5). |
| `KUDAWIKI_INIT_EMAIL`, `KUDAWIKI_INIT_NAME` | El primer superadmin. La contraseña no va aquí: se pide por consola. |

Lo demás (correo, directorio LDAP o Active Directory, respaldos fuera del servidor, marca de la
empresa) se configura después desde *Administración*.

## 3. Bajar la imagen y verificar su firma

```sh
docker compose pull
```

Antes del primer arranque, comprobar con [cosign](https://docs.sigstore.dev/cosign/system_config/installation/)
que la imagen la firmó el flujo de release de este repositorio y no otra persona:

```sh
cosign verify "ghcr.io/athomo001/kudawiki:$v" \
  --certificate-identity-regexp '^https://github.com/athomo001/kudawiki/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com > /dev/null && echo "firma ok"
```

La firma es del contenido de la imagen, así que vale para cualquiera de sus nombres (`1.0.0`, `1`,
`latest`). Con `verify-attestation --type cyclonedx` y los mismos parámetros se obtiene su SBOM.

Si `pull` responde `denied`, el paquete está privado en GitHub. Quien administra el repositorio lo
abre una sola vez (*perfil → Packages → kudawiki → Package settings → Change visibility → Public*),
o cada servidor entra con un token de solo lectura (`read:packages`):
`echo "$TOKEN" | docker login ghcr.io -u <usuario> --password-stdin`.

## 4. Crear la instalación y arrancar

```sh
docker compose run --rm init   # crea la base, la llave maestra y el superadmin; pide la contraseña
docker compose up -d
docker compose ps              # STATUS tiene que llegar a "healthy"
docker compose exec kudawiki /kudawiki version
```

`init` se corre una sola vez: con una base ya creada se niega.

**La llave maestra hay que sacarla del servidor ahora.** Queda en el volumen `kudawiki_keys`, y sin
ella los documentos sellados y los secretos guardados no se recuperan, ni siquiera desde un
respaldo (los respaldos no la llevan, a propósito). Lo recomendado es repartirla en partes entre
personas distintas, de modo que cualquiera 3 de 5 la reconstruyan:

```sh
docker compose exec kudawiki /kudawiki masterkey split --shares 5 --threshold 3
```

Después: entrar con el superadmin, activar el segundo factor en *Mi perfil* y correr
`docker compose exec kudawiki /kudawiki doctor --security` para ver qué falta.

## 5. Publicarla con TLS

El contenedor escucha solo en `127.0.0.1:8080` del servidor: desde la red se llega por un proxy
inverso en la misma máquina, que termina el TLS. Con [Caddy](https://caddyserver.com), que obtiene
el certificado solo:

```caddyfile
wiki.empresa.cl {
    reverse_proxy 127.0.0.1:8080
}
```

En `.env`, `KUDAWIKI_BASE_URL=https://wiki.empresa.cl` y `KUDAWIKI_TRUST_PROXY=true`, y luego
`docker compose up -d`. Con nginx o Traefik vale lo mismo: el proxy tiene que mandar
`X-Forwarded-For` y `X-Forwarded-Proto`. Sin proxy delante, `KUDAWIKI_TRUST_PROXY` va en `false`;
si no, cualquiera podría inventar su IP.

## Qué trae `compose.yaml`

Un servicio, `kudawiki` (el contenedor se llama igual), y dos volúmenes: `kudawiki_data` (base,
archivos y respaldos) y `kudawiki_keys` (las llaves). Viene endurecido:

- Sistema de archivos de solo lectura salvo `/data`, y `/tmp` en memoria.
- Usuario sin privilegios, sin capacidades (`cap_drop: [ALL]`) y sin escalada (`no-new-privileges`).
  La imagen no tiene shell ni gestor de paquetes.
- La llave montada de solo lectura. Solo `init` la monta con escritura, y no arranca con `up`.
- Topes de 512 MB de memoria y de 512 procesos.
- 60 s para detenerse: un respaldo en curso alcanza a terminar.
- Logs JSON con rotación (5 archivos de 10 MB): `docker compose logs -f kudawiki`.

## Uso diario

Todo se corre en la carpeta del `compose.yaml`. `docker compose exec kudawiki /kudawiki` sin más
lista todos los comandos.

```sh
docker compose exec kudawiki /kudawiki backup                    # respaldar ahora (el diario es a las 03:00)
docker compose exec kudawiki /kudawiki doctor                    # salud del contenido
docker compose exec kudawiki /kudawiki doctor --security         # revisar la instalación
docker compose exec kudawiki /kudawiki audit verify              # la cadena de la auditoría sigue íntegra
docker compose exec kudawiki /kudawiki admin unlock ana@acme.cl  # si nadie puede entrar
```

Los respaldos quedan en `/data/backups`, en el mismo volumen que los datos: sin un destino externo
configurado (*Administración → Respaldos*), copiarlos fuera del servidor es tarea de quien opera.
Para restaurar uno, con el servicio detenido:

```sh
docker compose stop kudawiki
docker compose run --rm kudawiki restore /data/backups/<archivo>.tar.zst
docker compose up -d
```

## Versiones

[Versionado semántico](https://semver.org/lang/es/): `MAYOR.MENOR.PARCHE`. Cada versión publica en
[Releases](https://github.com/athomo001/kudawiki/releases) los binarios, el SBOM, las sumas
firmadas y unas notas que dicen si trae migraciones de base y si se puede saltar versiones. La
imagen sale con varios nombres:

| Nombre | Apunta a | Para qué |
| :--- | :--- | :--- |
| `1.2.3` | Esa versión, siempre | Producción con actualizaciones decididas a mano. |
| `1.2` | La última `1.2.x` | Solo correcciones. |
| `1` | La última `1.x` | Lo que trae `.env.example`: dentro de una versión mayor se salta sin pasos intermedios. |
| `latest` | La última de todas | Probar. Puede saltar a una versión mayor sin aviso. |

## Actualizar

1. Leer las notas de la versión nueva en Releases.
2. Bajar la imagen y ver qué cambia en la base, sin tocarla:

   ```sh
   docker compose pull   # con una versión fija, cambiar antes KUDAWIKI_VERSION en .env
   docker compose run --rm kudawiki migrate --dry-run
   ```

3. `docker compose up -d`. Al arrancar con migraciones pendientes, la wiki respalda la base
   (`/data/backups/kudawiki-pre-migrate-<fecha>.tar.zst`) y las aplica en una transacción: si una
   falla, no arranca y la base queda como estaba.
4. Si las notas dicen que cambió el `compose.yaml`, bajarlo de la etiqueta nueva como en el paso 1.

**Volver atrás:** sin migraciones, basta con poner la versión anterior en `KUDAWIKI_VERSION` y
`docker compose up -d`. Con migraciones no hay vuelta automática: se restaura el respaldo
`kudawiki-pre-migrate-…` como arriba y después se vuelve a la versión anterior. Una versión vieja
se niega a arrancar contra una base más nueva.

## Probar una imagen propia

Con el repositorio clonado, `make image` construye `ghcr.io/athomo001/kudawiki:dev` con lo que hay
en el disco. Con `KUDAWIKI_VERSION=dev` en `deploy/.env`, `docker compose up -d` usa esa.

---

Para reportar una vulnerabilidad: [SECURITY.md](../SECURITY.md).
