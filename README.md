<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/logo/kudawiki-logo-oscuro.svg">
  <img alt="Logo de Kudawiki" src=".github/logo/kudawiki-logo.svg" width="96">
</picture>

# Kudawiki

**La wiki del trabajo.**

[Releases](https://github.com/athomo001/kudawiki/releases) · [Instalar con Docker](deploy/README.md) · [Seguridad](SECURITY.md) · [Cambios](CHANGELOG.md) · [Fases](FASES.md)

</div>

> [!NOTE]
> **Camino a v1.0.** Las fases F1 a F8 están construidas y la auditoría interna con pentest no dejó hallazgos altos abiertos. La v1.0.0 sale sola con el próximo push a `main` que pase el CI (ver [Versiones](#versiones)). Antes de instalar en producción, leer [SECURITY.md](SECURITY.md) y el [manual de instalación](deploy/README.md).

## Qué es

Kudawiki es una **wiki corporativa ligera y autocontenida** para organizaciones que necesitan **aislamiento estricto de la información entre áreas** (TI, Finanzas, RRHH…). Cada área es un espacio de trabajo independiente: sus páginas, archivos y búsquedas no existen para quien no es miembro. Todo va en **un solo binario** (backend, SQLite e interfaz) que corre en **un solo contenedor Docker**, sin servicios externos y sin internet.

## Qué hace hoy

### Áreas y permisos

- Áreas con roles de superadmin, admin de área, editor y lector. Lo que no se puede ver responde siempre igual que lo que no existe, sin delatar qué hay.
- Niveles de privacidad N1 a N3 por página: un nivel alto lee los de abajo, nunca al revés. *¿Quién puede ver esto?* lo muestra antes de publicar.
- Artículos compartidos entre áreas, con aprobación del área de origen.
- Ciclo de vida de las áreas (archivar, desactivar, borrar con gracia) y traslado de páginas entre áreas.

### Escribir y leer

- Editor Markdown y editor visual, con autoguardado, aviso de edición simultánea y plantillas.
- Markdown extendido: avisos, pestañas, pasos, fórmulas, diagramas Mermaid y texto oculto.
- Historial con diferencias y reversión, papelera, enlaces internos con *qué enlaza aquí*, etiquetas y árbol que se ordena arrastrando.
- **Propuestas**: un cambio se propone y su responsable lo aprueba, lo rechaza o lo ajusta.
- Espacio personal para borradores privados y publicación a un área.
- Vigencia con revisión periódica, acuse de lectura y cuestionarios.
- Búsqueda de texto completo dentro de lo que cada persona puede ver: raíces en español e inglés, tildes, filtros y *¿quisiste decir?*.

### Documentos sellados

- Cifrados con una contraseña propia del documento, distinta de la de la cuenta, junto con sus archivos.
- Bóveda personal, acceso de emergencia con la llave maestra (auditado y avisado al autor) y marca de agua en lo más sensible.

### Identidad

- Cuentas locales, LDAP y Active Directory (AD, Samba AD, OpenLDAP, FreeIPA, 389 DS) con sincronización y bajas automáticas.
- Segundo factor con TOTP o passkeys, sesiones visibles y recuperación de contraseña por correo.

### Administración

- Auditoría con cadena de hashes, copia a syslog y paquete de evidencia para auditores.
- Respaldos diarios con retención, copia cifrada a S3 y restauración que se prueba sola cada semana. Desde el panel se respalda, se descarga y se borra.
- Salud del contenido, `doctor --security`, métricas para Prometheus.
- Importa Wiki.js 2.x y carpetas Markdown; exporta un área completa.
- Marca de la empresa, modos claro, oscuro y rosa, interfaz en español e inglés, y fuentes de lectura accesibles.

## Instalar

Desde la imagen publicada en GitHub, sin clonar ni compilar. Se necesita Docker con Compose v2 en Linux (amd64 o arm64):

```sh
mkdir kudawiki && cd kudawiki
v=1.0.0   # la versión elegida en Releases
curl -fsSLO "https://raw.githubusercontent.com/athomo001/kudawiki/v$v/deploy/compose.yaml"
curl -fsSL "https://raw.githubusercontent.com/athomo001/kudawiki/v$v/deploy/.env.example" -o .env
chmod 600 .env                 # poner la URL y el correo y el nombre del primer superadmin
docker compose pull
docker compose run --rm init   # pide la contraseña: no se guarda en ningún archivo
docker compose up -d
```

Queda en <http://localhost:8080>. Para usarla en red se publica con TLS detrás de un proxy inverso, y la llave maestra se guarda fuera del servidor: sin ella, los documentos sellados no se pueden abrir. El manual completo, con la verificación de la firma, el proxy, la llave y cómo actualizar, está en **[deploy/README.md](deploy/README.md)**.

El contenedor se llama `kudawiki` y viene endurecido: solo lectura, sin privilegios ni capacidades, la llave montada aparte y de solo lectura, con límites de memoria y de procesos.

## Versiones

[Versionado semántico](https://semver.org/lang/es/), automático. Cuando un push a `main` pasa el CI, [`tag.yml`](.github/workflows/tag.yml) mira los commits desde la última versión:

| Commit | Versión |
| :--- | :--- |
| `feat!: …` o `BREAKING CHANGE:` en el cuerpo | Mayor: `1.4.2` → `2.0.0` |
| `feat: …` | Menor: `1.4.2` → `1.5.0` |
| `fix: …`, `perf: …` | Parche: `1.4.2` → `1.4.3` |
| `docs:`, `chore:`, `ci:`, `test:`, `refactor:` | No genera versión |

Cada versión publica un [release](https://github.com/athomo001/kudawiki/releases) con los binarios Linux, el SBOM, las sumas firmadas y unas notas que dicen si trae migraciones y si se puede saltar versiones. La imagen sale firmada en `ghcr.io/athomo001/kudawiki` como `1.4.2`, `1.4`, `1` y `latest`; `.env.example` usa `1`, que se actualiza dentro de la misma versión mayor sin pasos intermedios. `kudawiki version` dice qué versión corre.

## Operación

La consola va dentro del contenedor: `docker compose exec kudawiki /kudawiki <comando>`, y sin comando lista todos. Los de uso diario, respaldar y restaurar, están en [deploy/README.md](deploy/README.md#uso-diario):

```sh
docker compose exec kudawiki /kudawiki backup                        # respaldar ahora (el diario es a las 03:00)
docker compose exec kudawiki /kudawiki doctor --security             # revisar la instalación
docker compose exec kudawiki /kudawiki ldap sync --dry-run           # qué haría la sincronización con el directorio
docker compose exec kudawiki /kudawiki admin unlock ana@acme.cl      # si nadie puede entrar
docker compose exec kudawiki /kudawiki admin set-password ana@acme.cl
```

La marca de la empresa, el correo, el directorio LDAP o Active Directory y los respaldos fuera del servidor se configuran en *Administración*, con vista previa o prueba antes de guardar. Para reportar una vulnerabilidad: [SECURITY.md](SECURITY.md).

## Desarrollo

| Capa | Tecnología |
| :--- | :--- |
| Backend | Go (librería estándar + `chi`, `log/slog`) |
| Markdown | `goldmark` + `bluemonday` + `chroma`; editor visual Milkdown |
| Base de datos | SQLite en WAL + FTS5 (`modernc.org/sqlite`, sin CGO) |
| Frontend | Svelte 5 + Vite, embebido con `go:embed`; Node 26 LTS y pnpm solo para el build |
| Distribución | Binario único en un solo contenedor Docker; systemd como alternativa |

| Comando | Qué hace |
| :--- | :--- |
| `make test` | Compila la UI con pnpm y corre los tests de Go (con `-race`). |
| `make lint` · `make audit` | gofmt, vet, staticcheck · govulncheck, gosec y `pnpm audit`. |
| `make e2e` | Playwright con Chrome contra el binario real: flujos y accesibilidad. |
| `make image` | La imagen con lo que hay en el disco (`ghcr.io/athomo001/kudawiki:dev`); se prueba con `KUDAWIKI_VERSION=dev` en `deploy/.env`. |
| `make load` | Prueba de carga con 50 000 páginas; deja el resultado en `docs/rendimiento.md`. |
| `sh test/ldap/run.sh` | Pruebas contra Samba AD, OpenLDAP, 389 DS y FreeIPA en contenedores. |

Los mensajes de commit van en español con el prefijo de [Conventional Commits](https://www.conventionalcommits.org/es/) (`feat:`, `fix:`, `docs:`…): ese prefijo decide la versión.

## Estado

| Fase | Estado |
| :--- | :--- |
| F0 · Diseño y especificación | 🟡 En curso |
| F1 · Esqueleto | 🟡 Falta ver el CI verde en GitHub |
| F2 · Wiki con aislamiento | ✅ Hecha |
| F3 · Niveles, auditoría y respaldos | ✅ Hecha |
| F4 · Propuestas y notificaciones | ✅ Hecha |
| F5 · Espacio personal y vigencia | ✅ Hecha |
| F6 · Identidad, AD y 2FA | ✅ Hecha |
| F7 · Documentos sellados | ✅ Hecha |
| F8 · Endurecimiento y v1.0 | ✅ Hecha (la etiqueta v1.0.0 sale con el próximo push) |
| F8-B · Faltantes antes de v1.0 | ✅ Hecha |
| F9 · Hardening (SOC 2, ISO 27701, ISO 27001) | ⚪ Análisis de brechas hecho; los pasos esperan aprobación |
| F10 · Después de v1.0 | ⚪ Comentarios, PDF, gestión documental, MCP… |

Detalle en [FASES.md](FASES.md) · cambios en [CHANGELOG.md](CHANGELOG.md).

## El nombre

*Kudawiki* une *kuda* + *wiki*. *Kuda* (también escrita *kudaü* o *küdaw*) significa "trabajo" o "tarea" en mapudungun, la lengua mapuche. De la misma raíz vienen *küdawn* (trabajar) y *küdawfe* (trabajador o trabajadora).

El logo son tres capas apiladas, una por área: cada área de trabajo tiene su propio espacio, separado de las demás.

## Licencia

Privada: todos los derechos reservados. Ver [LICENSE](LICENSE).
