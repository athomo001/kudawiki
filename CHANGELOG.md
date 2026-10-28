# Changelog

Todos los cambios importantes de Kudawiki quedan anotados aquí.

El formato sigue [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/). Mientras no haya código, las versiones son las del **SPEC** (`spec-vX.Y`); cuando exista el binario se usará [versionado semántico](https://semver.org/lang/es/) (`vX.Y.Z`).

Categorías: **Agregado** · **Cambiado** · **Decidido** · **Descartado** · **Corregido** · **Seguridad**.

---

## [Sin publicar]

Lo que se haga desde aquí en adelante va en esta sección hasta la próxima versión del SPEC.

### Agregado
- **F3.4c: búsqueda, invitaciones y el resto de las pantallas.** **Ctrl+K y la página de resultados muestran la sección** donde está la coincidencia (*Conexión VPN › Instalación*) y el enlace salta a ese encabezado (§9.4, A3). **Página de resultados con filtros a la izquierda** y sus conteos (carpeta, etiquetas, autor, modificada en los últimos 7, 30 o 365 días) y **orden por relevancia o por fecha**. Elegir un filtro lo agrega a la consulta (`tag:`, `path:`, `autor:`, `desde:`), así la URL lo refleja; un valor con espacios va entre comillas (`autor:"Ana Soto"`). Los conteos salen de los mismos candidatos que la página (hasta 200) y solo cuentan lo que quien busca puede ver; la búsqueda FTS corre una sola vez por consulta. **Invitaciones** (§8.5.3, A7): quedan en la auditoría con `delivery: manual`, y desde el panel se genera un enlace nuevo para una invitación pendiente, vencida o no (`POST /admin/users/{id}/invite`), que anula el anterior; reenviar a quien ya eligió su contraseña responde `not_invited`. **Pantallas**: login e invitación con el logo, el historial con la página en las migas, y las pantallas de gestión (administración, miembros, auditoría, papelera, etiquetas) con los mismos títulos, tablas y formularios. El logo toma sus colores del tema: la versión oscura en la barra superior, y en el resto sigue el sistema, como el favicon.
- **Editor: ruta editable y divisor arrastrable** (§15, pedidos del dueño). La ruta de la página se cambia en la cabecera del editor: al guardar, la página se mueve con sus subpáginas, la ruta vieja redirige y los enlaces del área se actualizan; si la ruta está ocupada, el contenido queda guardado y se sigue editando. El divisor entre el Markdown y la vista previa se arrastra con el mouse o se mueve con las flechas (Inicio y Fin para los extremos, doble clic para volver al 50 %), y el ancho queda en el navegador.
- **F3.4b: editor con el diseño aprobado** (SPEC §15, mockup "Editor"). Pantalla completa con su propia cabecera oscura: volver, título, ruta y revisión que se edita (en una página nueva, la ruta se escribe ahí), Descartar y Guardar. Debajo, una barra con etiquetas, idioma del contenido y nivel; el Markdown a la izquierda, con encabezados en azul y marcas en ámbar, y la vista previa a la derecha; el resumen del cambio al pie. Cursor, selección y colores siguen el tema claro u oscuro. Lo que el mockup muestra de fases posteriores no aparece todavía: enviar a revisión, quién más está editando, borrador guardado (F4), modo visual (§19) y la barra de formato con "Insertar bloque" (B6, propuesta para F4). **Conflicto al guardar** (§4.1, A1): el `409` trae la revisión que ganó, quién la guardó y el diff de su versión a la tuya; el editor lo muestra arriba y ofrece guardar tu versión encima o descartar la tuya y cargar la guardada, sin perder lo escrito. Solo hay detalle si quien guarda puede leer la página, y leer así una N3 queda en la auditoría. **Diff con resaltado dentro de la línea** (§4.3, A2): se marcan las palabras que cambiaron, no la línea entera, y las líneas iguales lejos de un cambio se pliegan ("líneas sin cambios: 18"); lo usan el historial y el conflicto. Se declaran `@codemirror/language` y `@lezer/highlight`, que ya venían con CodeMirror (misma versión del lockfile).
- **F3.4a: marco de la interfaz con el diseño aprobado** (SPEC §15, "Kudawiki — Interfaz"). Barra superior oscura con el logo, selector de área, búsqueda al centro (abre Ctrl+K), selector ES/EN, botón `Aa` y menú de usuario; menú lateral azul con inicio del área, árbol con carpetas, página actual resaltada e insignias N2/N3, y abajo papelera, miembros y auditoría según el rol; migas de pan; lector con el artículo de 820 px, columna derecha (en esta página, etiquetas, qué enlaza aquí, quién editó y cuándo, revisión) y columna de acciones de íconos (editar, historial, ver código fuente, quién puede ver, más acciones con mover y borrar en diálogos). **Idioma**: el cambio ES/EN es inmediato, sin recargar, y queda en el perfil; los avisos del contenido también se traducen al momento (A8). **Preferencias de lectura**: fuente predeterminada, para dislexia (OpenDyslexic) o de alta legibilidad (Atkinson Hyperlegible) y espaciado amplio, guardadas por persona y aplicadas en todas sus áreas; con dislexia las cursivas se ven en negrita, y el código sigue en monoespaciada. `PATCH /me` (§14.1) acepta `locale` y `reading_prefs`, valida los valores (`invalid_preferences`) y no toca a nadie más; `GET /auth/me` devuelve las preferencias. Migración 0010: `users.reading_prefs_json`. Fuentes IBM Plex Sans y Mono, Atkinson Hyperlegible y OpenDyslexic (OFL, paquetes `@fontsource` con versión fija), solo el subconjunto latino, dentro del binario. Logo D · Capas en la barra y `favicon.svg` (`ui/static/`). Los bloques de código van siempre sobre fondo oscuro, como en el mockup (`chroma.css` solo con la paleta github-dark). En pantallas angostas las columnas se apilan; al imprimir solo sale el contenido.
- **F3.3: auditoría con cadena de hashes** (§11). Cada evento sella al anterior (SHA-256) y `kudawiki audit verify` detecta una fila cambiada, borrada o intercalada. Se registran el login, el logout, las invitaciones, las áreas y sus miembros, las páginas (crear, editar, revertir, mover, cambiar nivel, borrar, restaurar, purgar), la lectura de páginas N3, los intentos de ruta no disponible (§19.35) y las subidas de media. Lista cerrada de acciones (una que no está no compila, y cada una tiene su texto en ES y EN), registro después del éxito desde los servicios, la purga como evento del sistema sin actor, `meta` sin contenido y con los secretos redactados, copia del actor (email, nombre, IP) fuera del hash para poder seudonimizarla, y un trigger que impide cambiar filas o borrar las de los últimos 180 días. Retención `audit.retention_days` (730; `KUDAWIKI_AUDIT_RETENTION_DAYS`; menos de 180 se lee como 180) aplicada por la purga diaria. Pantallas: auditoría del superadmin (todas las áreas, con IP, sin rutas ni títulos) y del admin de área (su área, con la ruta de las páginas, sin IP), con filtros por persona, acción y fechas. La matriz de contención suma `GET /w/{ws}/audit`.
- **F3.2: papelera y purga.** Borrar una página (editor o admin) la manda a la papelera con sus subpáginas visibles, como una unidad; desde ahí el admin restaura cualquiera y un editor lo que él borró. Lo borrado desaparece del árbol, la búsqueda, las etiquetas, los backlinks, los alias, el historial y la media; su ruta queda libre, y restaurar sobre una ruta ocupada responde "esa ruta no está disponible". Cada página registra una revisión `deleted` y otra `restored`. Un job diario purga lo que lleva más de `trash.retention_days` (30; `KUDAWIKI_TRASH_RETENTION_DAYS`) con sus revisiones, su índice de búsqueda y sus archivos, manda a la papelera la media que nadie usó en 7 días y borra las búsquedas sin resultados de más de 90 días (§4.4, §4.5, §9.6). Interfaz: "Borrar" en el lector y pantalla de papelera. La matriz de contención suma la papelera, una página borrada cuya marca no puede aparecer fuera de ella, y una matriz aparte para `DELETE` (borra de verdad). Migración 0008: las migraciones que reconstruyen una tabla con hijas corren con las claves foráneas apagadas y verifican la integridad antes de confirmar.
- **F3.1: niveles de lectura.** Elegir N1, N2 o N3 en el editor (nadie asigna uno superior al suyo), herencia hacia abajo (las páginas nuevas nacen con el nivel de su padre y las subpáginas suben con él, cada una con su revisión), insignia 🔒 en el árbol y el lector, y "¿Quién puede ver esto?" con simulador para el admin del área. La matriz de contención suma el panel de acceso y el cambio de nivel.
- **F2.6: cierre de F2.** Matriz de contención sistemática: seis personas × los 22 endpoints del área × cuatro niveles, con el mismo 404 byte a byte donde no corresponde; barrido de fugas con marcas únicas en cada página (con control positivo, para que no pase en vacío); y un test que falla si una ruta del router no está en la matriz. Verificada con mutaciones. Prueba de carga de la búsqueda (`KUDAWIKI_LOAD=1`): 50 000 páginas, p95 44 ms. **F2 cumple su criterio de salida.**
- **F2.5: búsqueda.** FTS5 con los pesos de §9.1 (título, etiquetas, encabezados, contenido y código aparte), raíces Snowball en español e inglés, sin tildes, prefijos, frases, exclusiones, OR y filtros `tag:`, `path:`, `autor:`/`author:`, `desde:`/`since:`. "¿Quisiste decir?" por trigramas de títulos, búsquedas sin resultados (sin el usuario) para el admin del área, fragmentos que llegan como texto y nunca como HTML. El índice se escribe en la misma transacción del guardado y se reconstruye con `kudawiki search rebuild` o solo al arrancar si faltan páginas. Interfaz: Ctrl/Cmd+K y página de resultados. Idioma del contenido por página (`pages.lang`), elegido en el editor. La contención cubre resultados, sugerencias y conteos.
- **F2.4: etiquetas, enlaces y mover.** Etiquetas por página con nube y conteos que solo cuentan páginas visibles; enlaces `[[ruta]]` extraídos del AST al guardar, con enlaces rotos marcados (una página que el lector no ve sale rota, igual que una inexistente); "qué enlaza aquí" filtrado por nivel; mover o renombrar una página con su subárbol, con alias que redirigen (308) y reescritura de los enlaces de todo el área, todo en una transacción.
- **F2.3: extensiones de Markdown y media.** Avisos (`> [!NOTE]`), pestañas, pasos y desplegables (`:::`), `[[toc]]`, `[[children]]` (solo hijas visibles), teclas (`[[Ctrl+K]]`), enlaces `[[ruta|texto]]`, fórmulas con KaTeX como MathML y diagramas Mermaid en un iframe aislado con CSP propia. Media: subida con tipo detectado por contenido (sin SVG), miniaturas en Go puro con tope contra bombas de descompresión, pegar y arrastrar en el editor, CSP `sandbox` al servirla y herencia del nivel de su página. Clave nueva `upload.max_bytes` / `KUDAWIKI_UPLOAD_MAX_BYTES` (25 MB). Fuzzing del render con las extensiones: 458 mil entradas sin una fuga.
- **F2.2: páginas.** Crear y editar con bloqueo optimista (`base_revision`, 409 si otra persona guardó antes), árbol derivado de los slugs, render en servidor con goldmark + bluemonday + chroma (resaltado por clases, sin estilos en línea), índice lateral, historial inmutable con tipo de cambio, diff por líneas y revertir. Editor CodeMirror 6 con vista previa del mismo render, que se descarga solo al editar (el bundle de lectura pesa 27 KB comprimido). La matriz de contención cubre páginas, árbol, historial, revisiones y diff por nivel y por área. Fuzzing del render: 415 mil entradas sin una fuga.
- **F2.1: áreas, miembros e invitaciones.** El superadmin invita personas (enlace de un solo uso que se muestra una vez, §8.5.3), crea áreas y nombra admins; el admin de área gestiona lectores y editores. Capa `repo` con `Scope`, frontera de área y RoleGuard con 404 uniforme, y primera parte de la matriz de contención: otra área, lector, editor, ajeno y superadmin sin membresía reciben la misma respuesta que un área inexistente. Interfaz: inicio con mis áreas, panel de administración, miembros y aceptar invitación.
- **Primer código: esqueleto F1.** Binario `kudawiki` con `init`, `serve` y `healthcheck`; configuración por archivo y variables; SQLite en WAL con migraciones embebidas; MASTER_KEY; login local con Argon2id, política de contraseñas, sesiones, CSRF y rate limit; CSP estricta; `/healthz` y `/readyz`; logs JSON que registran el patrón de la ruta y nunca la ruta real; UI Svelte 5 con login e inicio en ES/EN. Imagen Docker de un solo contenedor (`Dockerfile`, `deploy/compose.yaml`, valores en `deploy/.env`), `Makefile` y CI. F1 arrancó con F0 todavía abierta (faltan mockups, `SECURITY.md` y §19.5, 10 y 11, que no tocan F1).
- **Cuestionarios** (SPEC §4.16): un editor o un admin agrega preguntas a un procedimiento o manual para comprobar que se entendió. Alternativa única, selección múltiple (todo o nada o puntaje parcial) y verdadero/falso, con explicación y enlace a la sección que responde cada pregunta. Configurable: intentos (1, 2, 10… o ilimitados), nota para aprobar, mejor/último/primer intento, espera entre intentos, tiempo estimado y tiempo límite, preguntas y alternativas revueltas, N preguntas al azar de un banco y qué se muestra al terminar. Historial de intentos para la persona y resultados por persona y por pregunta para quien asignó, con CSV e intentos extra. No aparecen en el árbol ni en la búsqueda.
- **Aperturas en el acuse de lectura** (§4.14): quien pide el acuse ve cuándo cada destinatario abrió la página y cuántas veces, además de si confirmó.
- Tablas `read_request_views`, `quizzes`, `quiz_versions`, `quiz_attempts`, `quiz_extra_attempts` y columna `read_requests.quiz_id` (§10.3); rutas de API (§14.1, §14.3); eventos de auditoría y notificaciones nuevas.
- Decisiones 25–28 (§19), resueltas el mismo día.
- Documentos internos (privados, no se versionan): glosario del dominio (`CONTEXT.md`), 7 ADR iniciales en `docs/adr/` y la configuración de tareas para agentes en `docs/agents/` (issues locales en `.scratch/`).
- Tabla de seguimiento obligatoria en cada tarea de un agente, con filas fijas para el mapa mental, el CHANGELOG, el README y la documentación (`.agents/AGENTS.md` §6).
- 29 skills y 8 agentes de terceros para el desarrollo (no entran al binario): Go idiomático (samber/cc-skills-golang, MIT), Svelte 5 oficial (sveltejs/mcp, MIT) y auditoría de seguridad, análisis estático y fuzzing (trailofbits/skills, CC-BY-SA-4.0). Cada una indica desde qué fase aplica, y las que chocan con el SPEC llevan un aviso de que mandan las reglas de Kudawiki.

### Cambiado
- **Revisión de trazabilidad** (`docs/trazabilidad.md`, privado): cada sección del SPEC con su fase y su estado en el código. Encontró 9 puntos a medias en lo ya hecho (F1–F3) y 17 decisiones sin ninguna fase; quedan propuestas para que el dueño las ubique. Desde ahora, toda decisión nueva, y todo lo que el dueño aprueba fuera del repo, entra también en esa tabla con su fase (`.agents/AGENTS.md` §0).
- El diseño aprobado no había quedado en el SPEC ni en las fases, y la interfaz se construyó sin aplicarlo. Ahora el SPEC §15 lo enlaza como `[DECIDIDO]`, y aplicarlo a todas las pantallas es la tarea F3.4 y parte del criterio de salida de F3.
- El acuse de lectura ya no es solo del admin de área: un **editor** puede pedirlo **a los lectores** sobre las páginas que puede editar; el admin sigue pudiendo pedirlo a toda el área (§3.1, §4.14).
- El acuse de lectura pasa de M7/F8 a **M4/F5**, junto con los cuestionarios (§18).

### Decidido
- **Editor visual con Milkdown** (§4.2): de las dos opciones que dejaba el SPEC, se elige Milkdown porque parte de Markdown y la ida y vuelta es más fiel. Sigue en F8.
- La ruta de la página se edita desde el editor, y el ancho entre el Markdown y la vista previa se ajusta con un divisor (§15).
- **Identidad visual aprobada** (2026-10-26) en el diseño "Kudawiki — Interfaz": barra superior oscura, menú lateral azul con el árbol, migas de pan, columna derecha, columna de acciones de íconos, selector de idioma y preferencias de lectura. Es la referencia de toda la interfaz (SPEC §15).
- Un editor no puede pedir acuse ni asignar cuestionarios a otros editores; solo a lectores (§19.25).
- Los resultados de los cuestionarios no se usan en evaluaciones de desempeño (§19.26).
- El acuse de lectura y los cuestionarios se construyen en F5 (M4) (§19.28).
- El frontend usa **pnpm** como gestor de paquetes, nunca npm ni yarn: un solo lockfile (`ui/pnpm-lock.yaml`), `pnpm install --frozen-lockfile` en el build y `pnpm audit` en CI (§15, §19.29, §21.2).
- El build del frontend usa **Node 26 LTS**; pnpm se instala como binario standalone con hash verificado, porque desde Node 25 no viene corepack (§15, §19.30).
- **Arquitectura SOLID y clean, sin código especulativo** (§16.2, ADR-0008): dependencias hacia el dominio, interfaces chicas declaradas por quien las usa, y nada (opciones, flags, endpoints, columnas, abstracciones) que el SPEC no pida para la fase en curso.
- **Despliegue con Docker, en un solo contenedor** (§13.11, §19.32): sin contenedores aparte de base de datos, frontend ni proxy; la MASTER_KEY va como secret de solo lectura, nunca en la imagen, el volumen ni una variable de entorno. El binario con systemd queda como alternativa. La imagen y el `compose.yaml` pasan a F1.
- **Licencia privada** por ahora: todos los derechos reservados (`LICENSE`, §19.31).
- Driver de SQLite `modernc.org/sqlite`, sin CGO (§13.4, §19.33).
- Cada página tiene su idioma de contenido (`es` o `en`), que decide las raíces de la búsqueda y el `lang` del lector (§9.1).
- Toda ruta no disponible (ocupada por una página visible u oculta, o bajo un padre que no se ve) responde lo mismo: "esa ruta no está disponible" (§19.35).
- Al mover una página, sus subpáginas que quien mueve no ve se quedan donde están (§19.36).
- Al borrar una página, sus subpáginas que quien borra no ve se quedan donde están, igual que al mover (§4.5, §19.37).
- Un editor restaura de la papelera lo que él borró; el admin del área, todo (§4.5, §19.38).
- Los logins fallidos van a la auditoría como el primer fallo de cada ventana de 15 minutos y el bloqueo con su conteo, solo para usuarios existentes (§11.2).
- `page.path_unavailable` no guarda la ruta pedida: se la revelaría al superadmin (§11.2).
- La auditoría del superadmin muestra ids de páginas, no rutas ni títulos; la IP la ve solo el superadmin (§11.2).
- `audit_log` guarda la IP solo en la copia del actor, fuera del hash, y la fecha como texto de ancho fijo (§10).
- La copia a syslog y la exportación JSONL/CSV de la auditoría pasan de F3 a F8; el modo auditoría del superadmin, a F6 (necesita la re-autenticación con 2FA).
- La ruta de una página es única solo entre las páginas vivas: borrar una deja su ruta libre, y restaurarla sobre una ruta ocupada responde `path_unavailable` (§4.5, §10).
- i18n del frontend propia y mínima, sin dependencias (§15, §19.34).
- El rol admin de un área lo da y lo quita solo el superadmin (§3.1, §19.10).
- El alta de usuarios con enlace manual pasa de F4 a F2: sin usuarios no se puede probar el aislamiento; el envío por correo sigue en F4 (§8.5.3).
- Las acciones sobre una página van como prefijo (`/history/{path}`, `/diff/{path}`), no como sufijo: con slugs de largo variable, una página llamada `history` chocaría con la ruta (§14.3).
- Mermaid se dibuja en un iframe aislado (`sandbox="allow-scripts"`, sin acceso a la sesión ni a la API) con su propia CSP; la CSP de la wiki no se afloja (§4.10).
- `page_revisions` acepta los siete tipos de cambio de §4.3: la migración 0003 solo permitía tres y SQLite no deja cambiar un `CHECK`, así que la 0005 reconstruye la tabla.
- Política de contraseñas relajada en desarrollo: con `base_url` en localhost basta una contraseña no vacía; con una URL real rige la completa, de 10 caracteres y fuera de la lista de comunes (§8.1).
- TypeScript 6 en `ui/`: `svelte-check` todavía no soporta TypeScript 7.

### Descartado
- Node 25 para el build: llegó a fin de vida el 2026-06-01 (§19.30).
- Preguntas abiertas con corrección manual en los cuestionarios (§19.27).

### Corregido
- En modo oscuro, los diagramas Mermaid salían dentro de un recuadro blanco: el iframe aislado no declaraba `color-scheme` y el navegador lo pintaba opaco. Ahora toma el fondo de la página (§4.10).
- `kudawiki init` fallaba en Docker con la migración 0007 ("disk I/O error"): SQLite necesitaba un archivo temporal y el contenedor es de solo lectura, y al servicio `init` le faltaba `/tmp`. Ahora SQLite guarda sus temporales en memoria (`temp_store=MEMORY`) y `init` también tiene `/tmp` en memoria.
- Guardar una página se volvía más lento mientras más páginas tenía el área: el índice de búsqueda se reescribía buscando por una columna sin índice. Ahora cada página ocupa una fila fija (`pages.search_rowid`, migración 0007). Sembrar 50 000 páginas pasó de no terminar en 22 minutos a 1 min 30 s.
- La búsqueda con 50 000 páginas tardaba 75 ms (p95): filtrar área y nivel dentro de FTS5 cargaba la fila entera de cada coincidencia, y el fragmento se calculaba para todas. Ahora filtra uniendo con `pages` (con el orden fijado por `CROSS JOIN` y un test del plan) y calcula el fragmento solo para lo que se muestra: 44 ms. Cada búsqueda tiene un tope de 3 s.
- La búsqueda con más de una palabra daba error 500: FTS5 no acepta el AND implícito entre grupos con paréntesis. Ahora se une con AND explícito, y un fuzz test ejecuta contra FTS5 real todo lo que arma la caja de búsqueda.
- En la interfaz, "Más resultados" reiniciaba la búsqueda y la lista de miembros no se recargaba al pasar de un área a otra: dos efectos de Svelte dependían de lo que no debían.
- El editor quedaba sin estilos y sin poder escribir (y podía colgar el navegador): CodeMirror inserta sus estilos en un `<style>` que la CSP estricta bloqueaba. Ahora cada `index.html` lleva un nonce que la CSP autoriza solo para estilos (§14.5).

### Seguridad
- Go fijado con `toolchain go1.27.3`: la 1.27.0 tenía 11 vulnerabilidades alcanzables en `net/http`, `crypto/tls` y `crypto/x509`.
- Las respuestas correctas nunca llegan al navegador antes de enviar el intento; intentos, espera y tiempo límite se controlan en el servidor; un solo intento abierto por persona (índice único).
- Los puntajes se tratan como datos personales de desempeño, con acceso y retención propios (§13.10).

---

## [spec-v0.6] — 2026-10-27

### Agregado
- **Marca de la empresa** (SPEC §15.1): el superadmin cambia el título del sitio, el nombre de la empresa, el logo (claro y oscuro), el favicon, el color principal, el de la barra superior y el mensaje del login. Contraste automático (AA), modo oscuro derivado, vista previa en vivo y "Restablecer a Kudawiki". La marca aparece en la interfaz, los correos (logo incrustado), la marca de agua, la impresión/PDF, el emisor del 2FA y el acceso directo del celular.
- **Recuperación de contraseña por correo** (§8.5): enlace de un solo uso válido 30 min, que no revela si la cuenta existe; cierra todas las sesiones y mantiene el 2FA. Invitaciones para usuarios locales nuevos, "enviar enlace para restablecer" desde el panel, enlace manual cuando no hay SMTP y cambio de contraseña desde el perfil.
- **Integración LDAP / Active Directory completa** (§8.6): login paso a paso (rechazo de contraseña vacía, escape RFC 4515, exactamente una entrada, dos conexiones), soporte de cualquier directorio LDAP (AD, OpenLDAP, FreeIPA, 389 DS y modo `generic` con detección automática), varios controladores con failover, mensajes para contraseña expirada, solo entran miembros de grupos mapeados, vinculación de cuentas locales, foto desde el directorio, configuración desde el panel, pruebas de conexión y de usuario, y log de depuración temporal.
- Tablas `auth_tokens`, `site_settings` y `site_assets` (§10.2); rutas de API nuevas (§14); lista de plantillas de correo y correo de prueba (§12.1).
- Decisiones 20–24 (§19), resueltas el mismo día.

### Cambiado
- El azul `#1D4F91` y el logo D · Capas pasan a ser los **valores por defecto**, no fijos (§15).
- La conexión LDAP, que antes era solo YAML, también se edita desde el panel; el archivo sigue ganando si define la clave (§8.6.4, §13.1).
- La marca de agua suma el logo y el nombre de la empresa (§4.15).

### Decidido
- Marca configurable por empresa (color, logo, favicon, título), también en los correos y la marca de agua.
- Recuperación de contraseña por correo.
- Integración LDAP revisada contra la de Wiki.js.
- Sin color ni marca por área: la marca es de toda la instalación (§19.20).
- "Con Kudawiki" visible por defecto en el pie (§19.21).
- Los superadmins también pueden recuperar su contraseña por correo (§19.22).
- Por LDAP solo entra quien está en un grupo mapeado; no es configurable (§19.23).
- LDAP tiene que funcionar con cualquier directorio desde v1 (§19.24).

### Descartado
- CSS, HTML o JavaScript personalizado en el tema (lo que hacen `injectCSS`/`injectHead`/`injectBody` de Wiki.js) (§15.1.5).

### Seguridad
- Token de recuperación en el fragmento de la URL (no llega a logs ni proxies), límites por email e IP, aviso "tu contraseña cambió".
- El rate limit de login se aplica antes de consultar AD, para que la wiki no sirva para bloquear cuentas del dominio.
- Los logos SVG se sanitizan y se sirven con CSP `sandbox`.

---

## [spec-v0.5] — 2026-10-25

### Agregado
- **Buscador rediseñado** (SPEC §9): búsqueda en todo a la vez con pesos (título 10, etiquetas 6, encabezados 4, descripción 3, contenido 1), raíces de palabras en español e inglés, tolerancia a errores de tipeo en títulos, sintaxis de filtros (`tag:`, `path:`, `autor:`, frases, exclusiones), Ctrl+K en dos niveles, objetivo p95 < 50 ms y registro de búsquedas sin resultados.
- **Preferencias de lectura** (§15): fuente para dislexia (OpenDyslexic), alta legibilidad (Atkinson Hyperlegible) y espaciado amplio, embebidas en el binario.
- **Mapeo contra ISO/IEC 27001:2022** (§21): 17 controles del Anexo A cubiertos y 8 brechas a cerrar antes de v1.0.
- **Logo** "D · Capas", versiones claro / oscuro / un color y favicon (`spec/logo/`).
- Origen del nombre en el SPEC y el README: *kuda* (trabajo, en mapudungun) + *wiki*.
- Mockups de la interfaz: Lector (interactivo, con idioma y preferencias de lectura), Editor, Bandeja de propuestas, Documento sellado y hoja del logo.
- Convenciones de código (§16.1).
- `FASES.md`, `CHANGELOG.md` y `spec/MAPA.md`.

### Cambiado
- La interfaz pasa de "español por defecto" a **español e inglés completos desde v1** (§15).
- El índice de búsqueda lo escribe la capa `repo` en Go dentro de la transacción del guardado, en vez de triggers de SQLite (necesario para las raíces de palabras).
- Los backups externos (S3/SFTP) pasan de fase 2 a v1 (§13.3).
- La copia del actor en la auditoría va en una columna aparte, fuera del hash de la cadena, para poder seudonimizarla (§11.2, §13.10).

### Decidido
- Logo D · Capas.
- Comentarios del código y documentación interna en español.
- Interfaz bilingüe ES/EN y fuente para dislexia.

### Seguridad
- TLS obligatorio fuera de `localhost`, HSTS y TLS en las conexiones salientes (§21.2).
- Escaneo de dependencias, SBOM, revisión de código obligatoria en las carpetas sensibles y pentest antes de v1.0 (§21.2).

---

## [spec-v0.4] — 2026-10-25

### Agregado
- "¿Quién puede ver esto?" por página, con simulador al cambiar el nivel (§3.4).
- Ciclo de vida de áreas: activa, archivada, desactivada, borrada con gracia; cambio de slug con alias (§3.5).
- Traslado de páginas y subárboles entre áreas, solo por el superadmin (§4.13).
- Acuse de lectura de políticas (§4.14) y marca de agua en N3 y sellados (§4.15).
- Rotación de la MASTER_KEY y custodia repartida con Shamir (§6.5).
- Regla de dos personas para las acciones más delicadas del superadmin (§6.6).
- "Mis sesiones" y aviso de inicio de sesión desde un dispositivo nuevo (§8.2).
- Recuperación por consola (`kudawiki admin …`) (§13.6).
- Actualizaciones seguras con backup previo a migraciones (§13.9).
- Datos personales y Ley 21.719 (§13.10).

### Decidido
- **Baja de usuarios** (§8.4): el contenido se transfiere al admin del área y no se borra; solo se borra el contenido personal no publicado.

### Descartado
- Antivirus en las subidas (integración externa, mucho trabajo para el valor).
- Cuotas de espacio.

---

## [spec-v0.3] — 2026-10-25

### Agregado
- Análisis de Wiki.js 2.x, `vega` y `scarlett`: qué se toma, qué se deja para después y qué se descarta (§20).
- Etiquetas (§4.6), enlaces internos y backlinks (§4.7), vigencia del contenido con dueño y revisión periódica (§4.8), plantillas (§4.9).
- Extensiones de Markdown: avisos, pestañas, pasos, Mermaid, KaTeX, índice de hijas (§4.10).
- Autoguardado en servidor y aviso de edición concurrente (§4.11).
- Diseño de comentarios, pendiente de decisión (§4.12).
- Excepciones de aprobación por subárbol y por página (§5.2.1).
- Passkeys (§8.1).
- Reglas de escritura de la auditoría y piso de retención de 180 días (§11.2).
- Notificaciones con categorías, seguimiento, agrupación y baja en un clic (§12.1).
- Salud del contenido (§13.7), exportación/importación de áreas e importador desde Wiki.js 2.x (§13.8).

### Cambiado
- Mover una página ahora reescribe los enlaces que apuntaban a ella (antes quedaba para fase 2).
- El rol `admin` de área solo lo asigna el superadmin (propuesta, §3.1).

---

## [spec-v0.2] — 2026-10-25

### Agregado
- Primera versión de `spec/SPEC.md` a partir del pre-spec y de `agregado.txt`.

### Decidido
- Niveles de lectura iguales a los roles (lector, editor, admin).
- Editor en modo Markdown y visual, guardando siempre Markdown.
- Papelera con borrado suave; los sellados se borran de forma inmediata y definitiva.
- Documentos sellados con contraseña propia, cifrado de sobre y restablecimiento aprobado por el superadmin.
- Mapeo de grupos de Active Directory a áreas y roles en v1.
- El admin de área no ve los accesos de otros admins ni del superadmin a los sellados.
- Autenticación local + LDAP/AD + TOTP; auditoría; backups automáticos; email.

---

## [spec-v0.1] — 2026-10-25

### Agregado
- `spec/pre-spec.txt`: visión, stack (Go + SQLite + Svelte, binario único), modelo de aislamiento por áreas y API base.
- `spec/agregado.txt`: modo draft con aprobación, niveles de privacidad, borradores secretos, documentos ultra seguros e historial de versiones.

[Sin publicar]: #sin-publicar
[spec-v0.5]: #spec-v05--2026-10-25
[spec-v0.4]: #spec-v04--2026-10-25
[spec-v0.3]: #spec-v03--2026-10-25
[spec-v0.2]: #spec-v02--2026-10-25
[spec-v0.1]: #spec-v01--2026-10-25
