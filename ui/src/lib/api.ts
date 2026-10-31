export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    // El cuerpo completo de "error": algunos traen más que el código (p. ej. el 409, SPEC §4.1).
    readonly detail: Record<string, unknown> = {},
  ) {
    super(code);
  }
}

export type Role = 'viewer' | 'editor' | 'admin';
// viewers_can_propose: el área deja a sus lectores proponer cambios (SPEC §19.5).
// require_change_summary: el área exige resumen al cambiar una página que existe (§4.3).
export type Area = { slug: string; name: string; description: string; role: Role; viewers_can_propose: boolean; require_change_summary: boolean };

// Preferencias de lectura de SPEC §15; se aplican en todas las áreas.
export type ReadingPrefs = { font: 'default' | 'dyslexic' | 'legible'; wide: boolean };

export type Me = {
  id: string;
  email: string;
  full_name: string;
  locale: 'es' | 'en';
  reading_prefs: ReadingPrefs;
  is_global_admin: boolean;
  workspaces: Area[];
  has_avatar: boolean;
};

export type Member = { user_id: string; email: string; full_name: string; role: Role };
export type Level = 1 | 2 | 3;
export type PageRef = { slug: string; title: string; level?: Level; description?: string; icon?: string };
export type Heading = { level: number; id: string; text: string };
export type Page = {
  slug: string;
  title: string;
  markdown: string;
  html: string; // ya saneado en el servidor (SPEC §4.1)
  toc: Heading[] | null;
  tags: string[] | null;
  lang: 'es' | 'en';
  level: Level;
  revision: number;
  updated_by: string;
  updated_at: string;
  description: string; // corta, para listados y búsqueda (§4.1)
  icon: string; // emoji, o vacío
  // Cómo la sigue quien lee (§12.1): sola, con sus subpáginas o no; watch_via, el padre que
  // sigue con sus subpáginas, si lo hay.
  watch: WatchMode;
  watch_via: string;
};

export type WatchMode = '' | 'page' | 'subtree';

// Un aviso de la campana (§12.1). page es la ruta actual de la página (vacía si se borró).
export type NotificationKind =
  | 'page_changed'
  | 'page_deleted'
  | 'proposal_pending'
  | 'proposal_reminder'
  | 'proposal_approved'
  | 'proposal_rejected'
  | 'proposal_rebase';
export type Notification = {
  id: string;
  kind: NotificationKind;
  area: { slug: string; name: string };
  page: string;
  proposal: string;
  title: string;
  actor: string;
  days: number | null;
  count: number;
  read: boolean;
  updated_at: string;
};

export type EmailPref = 'immediate' | 'daily' | 'off';
export const notificationCategories = ['proposals', 'my_proposals', 'watched'] as const;
export type NotificationCategory = (typeof notificationCategories)[number];
export type NotificationPrefs = {
  auto_watch: boolean;
  email: Record<NotificationCategory, EmailPref>;
  email_enabled: boolean;
};

// A dónde lleva un aviso: las mismas rutas que arma el correo (internal/notify).
export function notificationHref(n: Notification): string {
  const area = `/w/${n.area.slug}`;
  if (n.kind === 'proposal_pending' || n.kind === 'proposal_reminder') return `${area}/proposals`;
  if (n.kind.startsWith('proposal_')) return `${area}/proposals/${n.proposal}`;
  return n.page ? `${area}/p/${n.page}` : area;
}
export type RevisionMeta = {
  revision: number;
  kind: 'created' | 'updated' | 'moved' | 'deleted' | 'restored' | 'reverted' | 'level_changed';
  title: string;
  author: string;
  // Quien aprobó la propuesta de la que salió la revisión (SPEC §5.2); vacío si se publicó directo.
  approved_by: string;
  summary: string;
  created_at: string;
};
export type TrashItem = {
  id: string;
  slug: string;
  title: string;
  level: Level;
  pages: number;
  deleted_by: string;
  deleted_at: string;
  purge_at: string;
};
export type AuditEvent = {
  id: number;
  at: string;
  action: string;
  actor?: { name: string; email: string };
  ip?: string;
  workspace?: string;
  page?: string;
  target_type: string;
  target_id: string;
  meta: Record<string, unknown> | null;
};
// parts: solo en una línea cambiada con pareja; changed marca lo que se borró o agregó (SPEC §4.3).
export type DiffLine = { op: '=' | '+' | '-'; text: string; parts?: { text: string; changed?: boolean }[] };
// Lo que trae el 409 al guardar: la revisión que ganó y el diff de ella a lo que quisiste guardar.
export type Conflict = { revision: number; updated_by: string; updated_at: string; diff: DiffLine[] };
// Borrador del autoguardado (SPEC §4.11): lo que quien edita dejó sin publicar. DraftRef es una
// página nueva en la lista que se ofrece al abrir el editor, sin el contenido.
export type Draft = { slug: string; title: string; markdown: string; base_revision: number; new_page: boolean; updated_at: string };
export type DraftRef = { slug: string; title: string; updated_at: string };
export type PresenceEditor = { id: string; name: string };
// Propuesta de cambio (SPEC §5.2). base_revision 0 = página nueva.
export type ProposalStatus = 'pending' | 'merged' | 'rejected' | 'withdrawn';
export type Proposal = {
  id: string;
  slug: string;
  title: string;
  status: ProposalStatus;
  new_page: boolean;
  base_revision: number;
  current_revision: number;
  level: Level;
  summary: string;
  author: string;
  author_id: string;
  reviewer: string;
  comment: string;
  merged_revision: number;
  created_at: string;
  reviewed_at: string | null;
  // Ya le tocó el recordatorio al admin (approval.remind_after_days, §5.3); solo en los listados.
  overdue?: boolean;
};
// El detalle para revisar: los cambios contra la base y, si la página avanzó, contra la versión
// actual; result es lo que quedaría publicado.
export type ProposalDetail = Proposal & {
  markdown: string;
  lang: 'es' | 'en';
  html: string;
  changes: DiffLine[];
  page_changed: boolean;
  conflict: boolean;
  against_current: DiffLine[] | null;
  page_changes: DiffLine[] | null;
  result: string;
  result_title: string;
};
export type ApprovalOverride = { prefix: string; approval_mode: 'direct' | 'required' };
export type TagCount = { tag: string; count: number };
// Inicio del área (SPEC §4.17). html ya viene saneado y rendido para quien lee.
export type Announcement = { id: string; title: string; html: string; author: string; created_at: string; can_delete: boolean };
export type AreaStats = {
  pages: number;
  changes_30d: number;
  new_30d: number;
  recent: { slug: string; title: string; icon: string; updated_at: string }[];
  top_viewed: { slug: string; title: string; icon: string; views: number }[];
};
export type Access = {
  level: Level;
  members: { name: string; email: string; role: Role; can_read: boolean; can_read_simulated?: boolean }[];
};

// Rango de cada rol: el nivel más alto que puede leer y asignar (SPEC §3.2).
export const rankOf = (role: Role): Level => ({ viewer: 1, editor: 2, admin: 3 })[role] as Level;
export type SearchHit = {
  slug: string;
  title: string;
  snippet: { text: string; hit?: boolean }[];
  description?: string;
  icon?: string;
  section?: { id: string; text: string }; // dónde está la coincidencia (SPEC §9.4)
};
export type Facet = { value: string; count: number };
// Filtros de la página de resultados con sus conteos; solo cuentan lo que quien busca ve.
export type Facets = { tags: Facet[]; folders: Facet[]; authors: Facet[]; dates: { days: number; count: number }[] };
export type SearchResult = { results: SearchHit[]; more?: boolean; suggestion?: string; facets?: Facets };

// Mismo origen: el navegador manda la cookie de sesión y Sec-Fetch-Site (CSRF, SPEC §8.2).
export async function api<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/api${path}`, {
      method,
      // FormData (subida de archivos) va tal cual: el navegador pone el boundary del multipart.
      headers: body === undefined || body instanceof FormData ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined || body instanceof FormData ? body : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'network');
  }
  if (!res.ok) {
    const data = await res.json().catch(() => null);
    throw new ApiError(res.status, data?.error?.code ?? 'internal', data?.error ?? {});
  }
  // Sin cuerpo no es solo el 204: el 202 de "¿Olvidaste tu contraseña?" tampoco trae (§8.5.1).
  const text = await res.text();
  return text ? JSON.parse(text) : (undefined as T);
}

export type Upload = { id: string; name: string; mime: string; url: string };
// Un archivo en el navegador de archivos (SPEC §4.4): page es la página que lo tiene, si alguna.
export type PageLink = { slug: string; title: string };
export type MediaFile = Upload & { size: number; thumb: boolean; uploaded_by: string; created_at: string; page: PageLink | null };

// mediaMarkdown es como se escribe un archivo en la página: las imágenes se ven, lo demás se descarga.
export const mediaMarkdown = (f: Upload) => (f.mime.startsWith('image/') ? `![${f.name}](${f.url})` : `[${f.name}](${f.url})`);

export function codeOf(e: unknown): string {
  return e instanceof ApiError ? e.code : 'internal';
}

export const canEdit = (a: Area) => a.role !== 'viewer';
// canWrite: puede abrir el editor, para publicar o para proponer (un lector, si el área lo deja).
export const canWrite = (a: Area) => canEdit(a) || a.viewers_can_propose;

export function formatDate(iso: string, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso));
}

// "hace 3 días": con Intl según el idioma de la interfaz (SPEC §15).
const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31536000],
  ['month', 2592000],
  ['week', 604800],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
];

export function timeAgo(iso: string, locale: string, now = Date.now()): string {
  const s = (new Date(iso).getTime() - now) / 1000;
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  for (const [unit, secs] of units) if (Math.abs(s) >= secs) return rtf.format(Math.round(s / secs), unit);
  return rtf.format(Math.round(s), 'second');
}
