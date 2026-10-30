# Fases de Kudawiki

Cómo vamos y qué viene. El detalle de cada función está en [spec/SPEC.md](spec/SPEC.md); el mapa del producto en [spec/MAPA.md](spec/MAPA.md) y lo hecho en [CHANGELOG.md](CHANGELOG.md).

**Leyenda:** ✅ hecho · 🟡 en curso · ⚪ pendiente

| Fase | Nombre | Entregable | Estado |
| :--- | :--- | :--- | :---: |
| [F0](#f0--diseño-y-especificación) | Diseño y especificación | SPEC cerrado, mockups, logo | 🟡 |
| [F1](#f1--esqueleto-m0) | Esqueleto (M0) | Binario que arranca y permite iniciar sesión | 🟡 |
| [F2](#f2--wiki-con-aislamiento-m1) | Wiki con aislamiento (M1) | Wiki usable por áreas + tests de contención | ✅ |
| [F3](#f3--niveles-auditoría-y-respaldos-m2) | Niveles, auditoría y respaldos (M2) | Lo necesario para usarla con datos reales | ✅ |
| [F4](#f4--propuestas-y-notificaciones-m3) | Propuestas y notificaciones (M3) | Modo draft completo | ⚪ |
| [F5](#f5--espacio-personal-y-vigencia-m4) | Espacio personal y vigencia (M4) | Borradores privados, plantillas, revisión periódica, acuse de lectura y cuestionarios | ⚪ |
| [F6](#f6--identidad-ad-y-2fa-m5) | Identidad, AD y 2FA (M5) | Login con Active Directory, TOTP, passkeys, bajas | ⚪ |
| [F7](#f7--documentos-sellados-m6) | Documentos sellados (M6) | Cifrado de sobre, bóveda, llave maestra | ⚪ |
| [F8](#f8--endurecimiento-y-v10-m7) | Endurecimiento y v1.0 (M7) | **v1.0** lista para producción | ⚪ |
| [F9](#f9--después-de-v10) | Después de v1.0 | Comentarios, PDF, MCP… | ⚪ |

Cada fase termina cuando se cumple su **criterio de salida**, no cuando se acaba la lista. Lo que no alcance pasa a la fase siguiente y se anota en el CHANGELOG.

---

## F0 — Diseño y especificación

**Objetivo:** que el SPEC esté lo bastante cerrado para empezar a programar sin rehacer el modelo de datos.

**Hecho**
- [x] Pre-spec y primera versión del SPEC con las decisiones base (áreas, niveles, propuestas, sellados, espacio personal).
- [x] Investigación de Wiki.js 2.x (estable), `vega` y `scarlett` (3.x experimental); comparativa en SPEC §20.
- [x] Ampliaciones del SPEC: etiquetas, enlaces, vigencia, plantillas, Markdown extendido, autoguardado, notificaciones, auditoría endurecida, salud del contenido, importador Wiki.js.
- [x] Baja de usuarios, ciclo de vida de áreas, traslados, acuse de lectura, marca de agua, llave maestra, recuperación por consola, datos personales.
- [x] Buscador rediseñado (pesos, raíces de palabras, tipeos, filtros).
- [x] Interfaz en español e inglés y preferencias de lectura (OpenDyslexic, Atkinson, espaciado).
- [x] Mockups: Lector, Editor, Propuestas, Sellado — aprobados el 2026-10-26 en el diseño ["Kudawiki — Interfaz"](https://claude.ai/artifact/VdzikVaZkB7LTc3ffJSTmu) (SPEC §15, identidad visual). Aplicarlos a la interfaz real es F3.4.
- [x] Logo (D · Capas), favicon y origen del nombre.
- [x] Mapeo contra ISO 27001 (SPEC §21).
- [x] Marca de la empresa configurable (SPEC §15.1), recuperación de contraseña por correo (§8.5) e integración LDAP completa (§8.6) — spec v0.6.
- [x] Acuse de lectura pedido también por editores, con aperturas (§4.14), y cuestionarios con puntaje, historial e intentos configurables (§4.16) — spec v0.7 (sin publicar).

**Pendiente**
- [ ] Resolver las decisiones abiertas de SPEC §19 (las que bloquean el modelo de datos primero: 5, 10, 11, 15).
- [ ] Sección "Stack" en el SPEC con versiones. Ya decididos: driver SQLite (`modernc.org/sqlite`, §19.33), i18n propia (§19.34) y Milkdown para el editor visual (§4.2, 2026-10-28; se construye en F8).
- [x] Licencia del proyecto: privada, todos los derechos reservados (`LICENSE`, SPEC §19.31).
- [ ] `SECURITY.md`.
- [x] Infraestructura del repo: `go.mod` (toolchain fijado), estructura de §16, `Makefile` y CI (`.github/workflows/ci.yml`: gofmt, vet, staticcheck, tests con `-race`, govulncheck, `pnpm audit`, build de la imagen). Falta verlo verde en GitHub.
- [ ] Mockups que faltan: cuestionario (crear, responder, resultados), inicio personal, "quién puede ver esto", panel de superadmin (incluidas Apariencia y LDAP), recuperar contraseña, vista móvil, modo oscuro.

**Criterio de salida:** SPEC v1.0 congelado (los cambios posteriores pasan por el CHANGELOG), repo con CI verde sobre un `main.go` vacío.

---

## F1 — Esqueleto (M0)

**Objetivo:** el binario existe, arranca, crea su base de datos y deja entrar al primer superadmin.

- [x] Configuración (`kudawiki.yaml` + variables `KUDAWIKI_*`), solo con las claves que usa F1.
- [x] SQLite en WAL, pool de lectura + una conexión de escritura, migraciones embebidas (tablas `users` y `sessions`).
- [x] `kudawiki init`: BD, MASTER_KEY y primer superadmin; no se puede correr dos veces.
- [x] Chequeos de arranque idempotentes: MASTER_KEY presente y de 32 bytes, migraciones aplicadas. Los secretos por instalación (§13.6) llegan con la función que los usa.
- [x] Login local (Argon2id, política con lista de contraseñas comunes), sesiones (12 h / 7 días), CSRF, rate limit (10 / 15 min por IP+email).
- [x] Pipeline de middleware y cabeceras de seguridad (§14.5): RequestID + log + recovery, CSP estricta, CSRF, sesión.
- [x] Frontend Svelte (login e inicio) embebido con `go:embed`, i18n ES/EN montado desde el inicio. Node 26 LTS (`ui/.node-version`) y pnpm (`ui/pnpm-lock.yaml`, versión fijada en `packageManager`).
- [x] `/healthz`, `/readyz`, logs JSON.
- [x] Imagen Docker de un solo contenedor (`Dockerfile`, `deploy/compose.yaml`) y `kudawiki healthcheck` (§13.11).

**Criterio de salida:** `docker compose run --rm init` y `docker compose up` levantan la wiki en un solo contenedor (y el binario suelto también arranca en Linux); se inicia sesión y se cierra sesión desde el navegador.

**Pendiente para cerrar F1:** ver el CI verde en GitHub (falta el primer push). Probado en el navegador el 2026-10-27: init, login y logout en el contenedor.

---

## F2 — Wiki con aislamiento (M1)

**Objetivo:** una wiki usable por áreas, con el aislamiento probado desde el día uno.

- [x] Áreas, miembros y roles; capa `repo` que exige `Scope{WsID, MaxRank}`; frontera de área y RoleGuard con 404 uniforme; panel de superadmin (personas y áreas) y miembros para el admin de área. *(F2.1)*
- [x] Alta de usuarios por invitación con enlace manual, de un solo uso y válido 7 días (§8.5.3). *Adelantado desde F4: sin usuarios no se puede probar el aislamiento; el envío por correo sigue en F4.* *(F2.1)*
- [x] Páginas, árbol, render en servidor (goldmark + bluemonday + chroma), con bloqueo optimista y fuzzing del render. *(F2.2)*
- [x] Extensiones de Markdown (§4.10): avisos, pestañas, pasos, desplegables, `[[toc]]`, `[[children]]`, teclas, fórmulas (KaTeX como MathML) y Mermaid en un iframe aislado. *(F2.3)*
- [x] Mover y renombrar páginas con su subárbol, alias que redirigen y reescritura de los enlaces en todo el área (§4.1, §4.7). *(F2.4)*
- [x] Historial, diff por líneas, revertir. *(F2.2; falta el resaltado dentro de la línea, §4.3)*
- [x] Media: subida con detección de tipo, miniaturas en Go puro, pegar y arrastrar en el editor, servida con CSP `sandbox`; hereda el nivel de su página. *(F2.3; la papelera de huérfanas llegó con F3.2)*
- [x] Etiquetas con conteos filtrados por nivel, enlaces internos `[[ruta]]` con enlaces rotos marcados y "qué enlaza aquí". *(F2.4)*
- [x] Búsqueda §9: FTS5 con los pesos de §9.1, raíces en español e inglés, tildes, prefijos, frases, exclusiones, OR, filtros `tag:`/`path:`/`autor:`/`desde:`, "¿quisiste decir?" por trigramas, búsquedas sin resultados para el admin, índice reconstruible (`kudawiki search rebuild` y al arrancar). Fuzzing de la consulta contra FTS5 real. *(F2.5)* Pendiente: la sección del encabezado en Ctrl+K, los filtros con conteos en la página de resultados y la prueba de carga (p95 < 50 ms con 50 000 páginas, §9.5) en F2.6.
- [x] Interfaz: lector con índice, editor Markdown (CodeMirror 6 con vista previa del servidor, se carga solo al editar), árbol e historial con diff. *(F2.2)*
- [x] Interfaz de búsqueda: Ctrl/Cmd+K con resultados en vivo y visitadas hace poco, página de resultados paginada. *(F2.5)*
- [x] **Matriz de tests de contención** (§17): seis personas × los 22 endpoints del área × cuatro niveles (N1, N2, N3, inexistente), con 404 idéntico byte a byte donde no corresponde; barrido de fugas con marcas únicas en título, encabezados, contenido y etiquetas de cada página, con control positivo; y un test que falla si una ruta del router no está en la matriz. Verificada con mutaciones (quitar el filtro de nivel del árbol o de los backlinks la hace fallar). *(F2.6)*

**Criterio de salida:** dos áreas con contenido sembrado; ningún test de contención encuentra una fuga (rutas, búsqueda, media, etiquetas, backlinks).

**Cumplido el 2026-10-27** (`TestContainmentMatrix`, `TestEveryRouteIsInTheMatrix`). La búsqueda cumple además el objetivo de §9.5 con 50 000 páginas (p95 44 ms; medida de nuevo el 2026-10-29 de punta a punta por la API: p95 41 ms, ver SPEC §9.5). Pendientes de §9 que no bloqueaban la salida: la sección del encabezado en Ctrl+K y los filtros con conteos en la página de resultados (hechos en F3.4c).

---

## F3 — Niveles, auditoría y respaldos (M2)

**Objetivo:** lo mínimo para poner datos reales.

- [x] Niveles de lectura N1–N3: elegirlos en el editor (hasta el propio), herencia hacia abajo con revisión `level_changed`, páginas nuevas con el nivel de su padre, insignia 🔒 en el árbol y el lector. *(F3.1)*
- [x] Fugas por niveles resueltas (§19.35, §19.36): una sola respuesta para toda ruta no disponible, y al mover las páginas ocultas se quedan en su lugar. *(F3.1; registrado en la auditoría desde F3.3)*
- [x] "¿Quién puede ver esto?" (§3.4) con simulador de nivel, calculado con la misma regla que autoriza (`repo.CanRead`). *(F3.1)*
- [x] Papelera y purga (§4.5): borrar manda el subárbol visible a la papelera como una unidad (las ocultas se quedan, §19.37), restaura el admin o quien borró (§19.38), la ruta queda libre, y un job diario purga lo vencido con sus revisiones y archivos, manda a la papelera la media huérfana de más de 7 días y borra las `search_misses` de más de 90 (§9.6). *(F3.2; registrado en la auditoría desde F3.3)*
- [x] Auditoría con cadena de hashes (§11): lista cerrada de acciones que no compila fuera de la lista, registro después del éxito desde los servicios, eventos del sistema sin actor, meta sin contenido, copia del actor fuera del hash, logins fallidos sin inundar, piso de 180 días también en la BD, retención en la purga diaria, `kudawiki audit verify`, y vistas del superadmin y del admin de área con el filtro en `repo`. *(F3.3; la copia a syslog y la exportación JSONL/CSV pasan a F8, el modo auditoría a F6)*
- [x] Interfaz con el diseño aprobado (SPEC §15, ["Kudawiki — Interfaz"](https://claude.ai/artifact/VdzikVaZkB7LTc3ffJSTmu)): barra superior oscura, menú lateral azul con el árbol, migas de pan, columna derecha (índice, etiquetas, qué enlaza aquí, metadatos), columna de acciones de íconos, selector ES/EN y preferencias de lectura (`Aa`: OpenDyslexic, Atkinson, espaciado amplio; guardadas por usuario, fuentes embebidas). Se aplica a todas las pantallas que existen, también a las que no tienen mockup propio; lo de fases posteriores no aparece hasta su fase. *(F3.4: a, marco, lector, idioma, preferencias de lectura, fuentes y logo; b, editor, diff en el conflicto y dentro de la línea, ruta editable y divisor; c, resto de pantallas, sección y filtros de la búsqueda, reenviar invitaciones; d, descripción e ícono por página, configuración del área, extras del lector, atajos y favicon. El modo oscuro con conmutador espera su mockup, B4)*
- [x] Backups automáticos, backup previo a migraciones, `restore`. También `migrate --dry-run`, no arrancar contra un esquema más nuevo y el rate limit en toda la API. *(F3.5; el destino externo y el cifrado del respaldo son de F8)*
- [x] Salud del contenido (`kudawiki doctor` y la pantalla Salud): enlaces, árbol, media y búsqueda. *(Vigencia, sellados y propuestas se suman en F5, F7 y F4)*
- [x] Recuperación por consola (§13.6): `kudawiki admin unlock`, `set-password`, `create-superadmin` y `revoke-sessions`, auditados como evento del sistema `cli`. *(F3.6; `reset-2fa` llega con el 2FA en F6 y el aviso a los superadmins con las notificaciones en F4)*
- [x] Marca de la empresa: título, colores con contraste automático, logo y favicon con sus variantes, sin parpadeo (§15.1). Pantalla **Administración → Apariencia** con vista previa en vivo, paletas sugeridas y "Restablecer a Kudawiki"; lo que fija el archivo de configuración gana y se ve bloqueado. *(F3.6; las variantes para el correo y la marca de agua llegan con F4 y F7, la vista oscura del panel con el modo oscuro (B4), y Apariencia, sin mockup propio, usa el marco y los formularios del diseño aprobado)*
- [x] Ajustes que pidió el dueño al probar F3 (2026-10-29): **Mi perfil** con foto, nombre, áreas, idioma, lectura y **cambio de contraseña** (§8.5.2, adelantado de F4); al entrar, directo a la wiki y selector de área solo con más de una; lector a todo el ancho, sin el título repetido y con **Escuchar** (voces del equipo); en el editor, **Elegir carpeta** (también en "Mover"), **Guía de Markdown**, `Ctrl+B` / `Ctrl+I` (§15) y **columnas sincronizadas**, con clic en la vista previa para ir a esa línea (§4.2); **avisos de Wiki.js** (`{.is-info}`) y botón **Copiar** en los bloques de código, y tablas que no cortan palabras ni código por la mitad (§4.10); **inicio del área** con portada, anuncios, actividad y más vistas (§4.17). *(F3.7; el inicio del área no tiene mockup propio y usa el marco y los formularios del diseño aprobado; el aviso por correo del cambio de contraseña llega con F4)*

**Criterio de salida:** restaurar un backup en otra máquina y que `audit verify` pase; los tests de contención cubren los tres niveles; todas las pantallas siguen el diseño aprobado.

**Cumplido el 2026-10-29.** Un respaldo restaurado en otra carpeta pasa `audit verify` (F3.5, `TestRestoredBackupPassesAuditVerify`), la matriz de contención cubre N1, N2 y N3 (F3.1) y todas las pantallas siguen el diseño aprobado (F3.4; Apariencia con el mismo marco). Pasaron a otras fases: syslog y exportación de la auditoría, destino externo y cifrado de los respaldos (F8); modo auditoría y `admin reset-2fa` (F6); modo oscuro con conmutador (B4, espera su mockup).

---

## F4 — Propuestas y notificaciones (M3)

- [ ] Propuestas: ciclo completo, diff, merge a 3 vías, aprobar con cambios, excepciones por subárbol.
- [ ] Notificaciones in-app y email: outbox, agrupación, ventana de email, baja en un clic.
- [ ] Seguir páginas.
- [ ] Correos con la marca de la empresa (logo incrustado) y correo de prueba.
- [ ] Recuperación de contraseña, envío de invitaciones por correo y el aviso *"Tu contraseña cambió"* (§8.5). El alta con enlace manual ya está desde F2 y el cambio de contraseña desde el perfil, desde F3.7.
- [ ] Autoguardado en servidor y aviso de edición concurrente.

**Criterio de salida:** un área en modo `required` funciona de punta a punta con dos personas reales; un usuario local recupera su contraseña por correo.

---

## F5 — Espacio personal y vigencia (M4)

- [ ] Espacio personal y publicación a un área (directo o como propuesta).
- [ ] Plantillas por área y globales.
- [ ] Vigencia del contenido: dueño, intervalo de revisión, verificación, cola de revisión.
- [ ] Acuse de lectura (§4.14): solicitudes de admins y de editores a lectores, aperturas, recordatorios, panel y CSV. *Adelantado desde F8.*
- [ ] Cuestionarios (§4.16): editor de preguntas, versiones, intentos con orden revuelto y tiempo límite en servidor, historial, resultados por persona y por pregunta, intentos extra.

**Criterio de salida:** un borrador personal llega a un área con su historial; una página vencida avisa a su dueño; un editor asigna un procedimiento con cuestionario a los lectores y ve quién lo abrió, quién aprobó y qué pregunta se falla más.

---

## F6 — Identidad, AD y 2FA (M5)

- [ ] TOTP, passkeys, reauth, "mis sesiones".
- [ ] LDAP/AD: login, JIT, mapeo de grupos, sincronización periódica, salvaguardas.
- [ ] LDAP: configuración desde el panel, pruebas de conexión y de usuario, cualquier directorio (`ad`, `openldap`, `generic` con detección automática), solo grupos mapeados, vinculación de cuentas locales (§8.6).
- [ ] Baja de usuarios (§8.4) con período de gracia.

**Criterio de salida:** probado contra un AD real o Samba AD DC, OpenLDAP, FreeIPA y 389 DS en contenedores, incluida una baja en AD que transfiere el contenido; la conexión se configura y se diagnostica desde el panel.

---

## F7 — Documentos sellados (M6)

- [ ] Cifrado de sobre (DEK por documento), bóveda por usuario, grants.
- [ ] Acceso de admins con MASTER_KEY, auditado y avisado al autor.
- [ ] Desclasificar, restablecer contraseña, borrado definitivo.
- [ ] Rotación de MASTER_KEY y custodia Shamir.
- [ ] Marca de agua en N3 y sellados.

**Criterio de salida:** con solo `wiki.db` + backups (sin MASTER_KEY) no se recupera texto de ningún sellado (test de grep sobre los archivos).

---

## F8 — Endurecimiento y v1.0 (M7)

- [ ] Brechas ISO 27001 (§21.2): TLS obligatorio, backups externos + verificación semanal, revisión de accesos, paquete de evidencia, guía de endurecimiento, SBOM.
- [ ] Auditoría: copia a syslog/journald (`audit.forward`) y exportación JSONL/CSV (§11). *(pasaron de F3.3)*
- [ ] Editor visual (WYSIWYG → Markdown).
- [ ] Ciclo de vida de áreas y traslado entre áreas (el acuse de lectura pasó a F5).
- [ ] Exportar/importar área, importador Wiki.js 2.x, export de datos personales.
- [ ] Pulido de UX, accesibilidad, documentación de operación.
- [ ] **Pentest externo.**

**Criterio de salida:** pentest sin hallazgos altos abiertos, pruebas E2E verdes, documentación de instalación y operación completa → se etiqueta **v1.0**.

---

## F9 — Después de v1.0

Sin orden fijo; se prioriza según el uso real: regla de dos personas, comentarios, PDF, búsqueda en adjuntos, draw.io/Excalidraw, espejo Git de salida, tokens de API + servidor MCP, "¿te sirvió esta página?", búsquedas sin resultados a la vista de más personas (la actividad y las más vistas se adelantaron a F3.7, §4.17).
