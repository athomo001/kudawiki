# Kudawiki

> [!WARNING]
> **Proyecto en preparación — Ultra Alpha.**
> Por ahora solo se está armando la infraestructura y el diseño de lo que se construirá en los próximos días. Todavía no hay código funcional, ni releases, ni instrucciones de instalación. Todo lo descrito aquí puede cambiar.

---

## ¿Qué es Kudawiki?

Kudawiki es una **wiki corporativa ligera y autocontenida** pensada para organizaciones que necesitan **aislamiento estricto de la información entre áreas o departamentos** (TI, Finanzas, RRHH, etc.).

Cada área funciona como un espacio de trabajo (*workspace*) independiente: sus documentos, archivos multimedia y búsquedas no son visibles para las demás áreas, salvo que un usuario tenga acceso explícito.

La meta es que todo se despliegue como **un único binario**, con bajo consumo de recursos y sin dependencias externas.

## Objetivos

- **Aislamiento por área:** cada documento, imagen o archivo pertenece a un workspace y solo lo ven sus miembros.
- **Roles claros:** Superadmin global, Admin de área, Editor y Lector.
- **Cero exposición estática:** ningún archivo se sirve sin pasar antes por la autorización.
- **Búsqueda de texto completo** limitada al área del usuario.
- **Historial de versiones** de cada página, con diffs y reversión.
- **Despliegue simple:** un solo ejecutable que incluye backend, base de datos y frontend.

## Funcionalidades planificadas

- Modo **borrador con aprobación**: un usuario propone cambios a un documento y su superior los aprueba, rechaza o ajusta.
- **Niveles de privacidad** jerárquicos: un nivel superior puede leer lo de los niveles inferiores, pero no al revés.
- **Borradores privados**: documentación personal que solo ve su autor (y el superadmin).
- **Documentos ultra seguros**: protegidos con una contraseña propia del documento, distinta de la del usuario.

## Stack tecnológico previsto

| Capa | Tecnología |
| :--- | :--- |
| Backend | Go (librería estándar + router `chi`) |
| Markdown | `goldmark` (CommonMark + tablas + sanitización) |
| Base de datos | SQLite embebido (modo WAL) + búsqueda FTS5 |
| Frontend | Svelte (SPA compilada con Vite) |
| Distribución | Binario único con frontend y migraciones embebidas (`go:embed`) |

## Estado

| Etapa | Estado |
| :--- | :--- |
| Investigación y especificación | 🟡 En curso |
| Infraestructura base del repositorio | 🟡 En curso |
| Backend (auth, workspaces, páginas) | ⚪ Pendiente |
| Frontend | ⚪ Pendiente |
| Primera versión usable | ⚪ Pendiente |

---

*Este README se irá actualizando a medida que el proyecto avance.*
