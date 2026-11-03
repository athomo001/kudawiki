<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="spec/logo/kudawiki-logo-oscuro.svg">
  <img alt="Logo de Kudawiki" src="spec/logo/kudawiki-logo.svg" width="96">
</picture>

# Kudawiki

**La wiki del trabajo.**

</div>

> [!NOTE]
> **Camino a v1.0.** Las fases F1 a F8 están construidas y la auditoría interna con pentest no dejó hallazgos altos abiertos. Falta etiquetar la versión. Antes de instalar en producción, leer [SECURITY.md](SECURITY.md) y la guía de operación (`docs/operacion/`).

---

## ¿Qué es Kudawiki?

Kudawiki es una **wiki corporativa ligera y autocontenida** pensada para organizaciones que necesitan **aislamiento estricto de la información entre áreas o departamentos** (TI, Finanzas, RRHH, etc.).

Cada área funciona como un espacio de trabajo (*workspace*) independiente: sus documentos, archivos multimedia y búsquedas no son visibles para las demás áreas, salvo que un usuario tenga acceso explícito.

La meta es que todo se despliegue como **un único binario**, con bajo consumo de recursos y sin dependencias externas.

## El nombre

*Kudawiki* une *kuda* + *wiki*. *Kuda* (también escrita *kudaü* o *küdaw*) significa "trabajo" o "tarea" en mapudungun, la lengua mapuche. De la misma raíz vienen *küdawn* (trabajar) y *küdawfe* (trabajador o trabajadora).

El logo son tres capas apiladas, una por área: cada área de trabajo tiene su propio espacio, separado de las demás.

## Objetivos

- **Aislamiento por área:** cada documento, imagen o archivo pertenece a un workspace y solo lo ven sus miembros.
- **Roles claros:** Superadmin global, Admin de área, Editor y Lector.
- **Cero exposición estática:** ningún archivo se sirve sin pasar antes por la autorización.
- **Búsqueda de texto completo** limitada al área del usuario.
- **Historial de versiones** de cada página, con diffs y reversión.
- **Despliegue simple:** un solo ejecutable que incluye backend, base de datos y frontend, corriendo en un solo contenedor Docker.

## Algunas funcionalidades

- Modo **borrador con aprobación**: un usuario propone cambios a un documento y su superior los aprueba, rechaza o ajusta.
- **Niveles de privacidad** jerárquicos: un nivel superior puede leer lo de los niveles inferiores, pero no al revés.
- **Borradores privados**: documentación personal que solo ve su autor (y el superadmin).
- **Documentos ultra seguros**: protegidos con una contraseña propia del documento, distinta de la del usuario.

## Stack tecnológico

| Capa | Tecnología |
| :--- | :--- |
| Backend | Go (librería estándar + router `chi`) |
| Markdown | `goldmark` (CommonMark + tablas) + `bluemonday` (sanitización) + `chroma` (código); editor visual Milkdown |
| Base de datos | SQLite embebido (modo WAL) + búsqueda FTS5 |
| Frontend | Svelte (SPA compilada con Vite; Node 26 LTS y pnpm solo para el build) |
| Distribución | Binario único con frontend y migraciones embebidas (`go:embed`), en un solo contenedor Docker |

## Estado

| Fase | Estado |
| :--- | :--- |
| F0 · Diseño y especificación | 🟡 En curso |
| F1 · Esqueleto | 🟡 Falta ver el CI verde en GitHub (primer push) |
| F2 · Wiki con aislamiento | ✅ Hecha |
| F3 · Niveles, auditoría y respaldos | ✅ Hecha |
| F4 · Propuestas y notificaciones | ✅ Hecha |
| F5 · Espacio personal y vigencia | ✅ Hecha |
| F6 · Identidad, AD y 2FA | ✅ Hecha |
| F7 · Documentos sellados | ✅ Hecha |
| F8 · Endurecimiento y v1.0 | ✅ Hecha (falta etiquetar v1.0) |
| F8-B · Faltantes antes de v1.0 | ✅ Hecha: respaldos y borrar todo en el panel, borrar y descargar respaldos, modos de color, Mi perfil |
| F9 · Hardening (SOC 2, ISO 27701, ISO 27001) | ⚪ Análisis de brechas hecho ([docs/seguridad/brechas-f9.md](docs/seguridad/brechas-f9.md)); los pasos esperan aprobación |
| F10 · Después de v1.0 | ⚪ Comentarios, PDF, gestión documental, MCP… |

Detalle de cada fase en [FASES.md](FASES.md) · cambios en [CHANGELOG.md](CHANGELOG.md) · mapa del producto en [spec/MAPA.md](spec/MAPA.md) · especificación completa en [spec/SPEC.md](spec/SPEC.md).

## Cómo correrlo

Kudawiki corre en **un solo contenedor Docker**:

```sh
cd deploy
cp .env.example .env      # y poner el correo y el nombre del primer superadmin
docker compose build
docker compose run --rm init   # pide la contraseña: no se guarda en ningún archivo
docker compose up -d
```

Queda en <http://localhost:8080>. Respalda el volumen `keys` (la llave maestra) **aparte** del volumen `data`: sin la llave, los documentos sellados no se pueden abrir.

### Respaldos

Kudawiki se respalda sola cada día a las 03:00 (`KUDAWIKI_BACKUP_TIME`) en el volumen `data` (`/data/backups`), y guarda 7 diarios, 4 semanales y 6 mensuales. Antes de cada actualización que cambia la base, hace además un respaldo propio (`kudawiki-pre-migrate-…`); de esos quedan los 3 últimos. Los demás se borran desde *Administración → Respaldos*, donde también se descargan. Cada respaldo trae la base y los archivos subidos, con el SHA-256 de cada parte; **la llave maestra no va adentro**.

```sh
docker compose exec kudawiki /kudawiki backup                 # uno ahora
docker compose stop kudawiki                                   # restaurar: con el servicio detenido
docker compose run --rm kudawiki restore /data/backups/<archivo>.tar.zst
docker compose run --rm kudawiki audit verify                  # la cadena de auditoría sigue íntegra
docker compose up -d
```

En otra máquina, se monta el archivo: `docker compose run --rm -v "$PWD/<archivo>.tar.zst:/restore.tar.zst:ro" kudawiki restore /restore.tar.zst`. Lo que había antes no se borra: queda en `/data/before-restore-<hora>`. Con un destino S3 compatible configurado (`backup.remote.*`), cada respaldo se sube cifrado fuera del servidor y una vez por semana se restaura uno de prueba en una carpeta temporal. Sin destino externo, los respaldos quedan en el mismo volumen que los datos y **copiarlos fuera es tarea de quien opera**. RPO, RTO y el procedimiento completo están en `docs/operacion/respaldos-y-recuperacion.md`.

Para revisar la salud del contenido (enlaces rotos, archivos que faltan, índice de búsqueda): `docker compose exec kudawiki /kudawiki doctor`, o la pantalla **Salud** del admin del área y del superadmin. Para revisar la instalación (permisos, llave maestra, TLS, 2FA de los admins, respaldos): `docker compose exec kudawiki /kudawiki doctor --security`.

### Marca de la empresa

El título, los colores, el logo y el favicon se cambian en **Administración → Apariencia** (solo el superadmin), con vista previa antes de guardar. También se pueden fijar en `kudawiki.yaml` (`branding.title`, `branding.color_primary`…) o con `KUDAWIKI_BRANDING_*`; lo que esté ahí gana y el panel lo muestra bloqueado.

### Directorio de la empresa (LDAP / Active Directory)

La conexión se configura en **Administración → Directorio** (solo el superadmin), con *Probar conexión* y *Probar un usuario* antes de guardar. Funciona con Active Directory, Samba AD, OpenLDAP, FreeIPA y 389 DS. Entra solo quien está en un grupo con una regla (grupo → área y rol). La sincronización corre sola cada hora (`KUDAWIKI_AUTH_LDAP_SYNC_INTERVAL`), crea de antemano las cuentas de quienes están en esos grupos y da de baja, con la gracia de la baja de usuarios, a quien el directorio deshabilita o saca de los grupos. Si una sincronización quitaría demasiados accesos de golpe, se detiene sin aplicar nada y avisa por correo a los superadmins.

```sh
docker compose exec kudawiki /kudawiki ldap sync --dry-run   # qué haría, sin tocar nada
docker compose exec kudawiki /kudawiki ldap sync             # sincronizar ahora
```

### Si nadie puede entrar

Con acceso al servidor, la consola resuelve los casos en que ningún superadmin puede iniciar sesión. Cada comando queda en la auditoría:

```sh
docker compose exec kudawiki /kudawiki admin unlock ana@acme.cl         # levanta el bloqueo por intentos fallidos
docker compose exec kudawiki /kudawiki admin set-password ana@acme.cl   # pide la contraseña nueva y cierra sus sesiones
docker compose exec kudawiki /kudawiki admin create-superadmin soporte@acme.cl "Soporte TI"   # cuenta de emergencia
docker compose exec kudawiki /kudawiki admin revoke-sessions --all      # o un correo
```

Para desarrollar: `make test` (compila la UI con pnpm y corre los tests), `make lint`, `make audit` (govulncheck, gosec y pnpm audit), `make e2e` (Playwright con Chrome contra el binario real: flujos y accesibilidad), y `make load` (prueba de carga con 50 000 páginas; deja el resultado en `docs/rendimiento.md`). Las pruebas contra directorios reales (Samba AD, OpenLDAP, 389 DS y FreeIPA en contenedores) se corren con `sh test/ldap/run.sh`: la primera vez FreeIPA tarda varios minutos en instalarse.

## Licencia

Privada: todos los derechos reservados. Ver [LICENSE](LICENSE).

---

*Este README se irá actualizando a medida que el proyecto avance.*
