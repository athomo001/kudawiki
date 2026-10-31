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
// personal: el espacio personal de quien entra (§7); audit: el de otra persona, que el superadmin
// lee en modo auditoría. Los arma la interfaz: /auth/me no los lista entre las áreas.
export type Area = {
  slug: string;
  name: string;
  description: string;
  role: Role;
  viewers_can_propose: boolean;
  require_change_summary: boolean;
  personal?: boolean;
  audit?: boolean;
};

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
  personal: { slug: string };
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
  review: Review;
  // Solo en el espacio personal (§7): lo que se mandó desde esta página a un área y en qué quedó.
  sent?: Sent[];
  // Las solicitudes de lectura abiertas que quien lee tiene sobre la página (§4.14).
  reading: MyRead[];
};

export type MyRead = {
  id: string;
  statement: string;
  requested_by: string;
  revision: number;
  due_at: string | null;
  confirmed_at: string | null;
  confirmed_revision: number;
  // La solicitud se cumple aprobando este cuestionario (§4.16); vacío, basta con confirmar.
  quiz_id: string;
};

// Solicitud de lectura (§4.14). audience: todo el área, unos roles o unas personas.
export type Audience = { all?: boolean; roles?: Role[]; users?: string[] };
export type ReadRequest = {
  id: string;
  page: { slug: string; title: string; level: Level };
  revision: number;
  statement: string;
  audience: Audience;
  due_at: string | null;
  created_by: string;
  created_at: string;
  closed_at: string | null;
  quiz_id: string;
  // Solo en el listado.
  recipients?: number;
  opened?: number;
  confirmed?: number;
};
export type ReadStatus = 'unopened' | 'opened' | 'confirmed' | 'overdue' | 'in_progress' | 'passed' | 'failed_out';
export type ReadPerson = {
  user_id: string;
  name: string;
  email: string;
  role: Role;
  status: ReadStatus;
  first_opened_at: string | null;
  last_opened_at: string | null;
  opens: number;
  confirmed_at: string | null;
  confirmed_revision: number;
  // Con cuestionario: intentos usados, extra que le dieron y su mejor puntaje.
  attempts: number;
  extra_attempts: number;
  best_percent: number;
};
export type ReadRequestDetail = ReadRequest & { people: ReadPerson[] };

// Cuestionarios (§4.16). El borrador (QuizDraft) lleva las respuestas correctas y solo llega a
// quien lo administra; quien responde ve QuizSummary y AttemptView.
export type QuizKind = 'single' | 'multiple' | 'true_false';
export type QuizOption = { id: string; text: string; correct: boolean };
export type QuizQuestion = { id: string; kind: QuizKind; prompt_md: string; points: number; explanation_md: string; anchor: string; options: QuizOption[] };
export type QuizSettings = {
  max_attempts: number; // 0 = ilimitados
  pass_percent: number;
  score_policy: 'best' | 'last' | 'first';
  cooldown_min: number;
  time_limit_s: number; // 0 = sin límite
  estimated_min: number; // 0 = automático
  shuffle_questions: boolean;
  shuffle_options: boolean;
  pick: number; // 0 = todas
  feedback: 'score' | 'wrong' | 'all';
  reveal: 'never' | 'after_final' | 'always';
  multiple_scoring: 'all_or_nothing' | 'partial';
};
export type QuizDraft = { settings: QuizSettings; questions: QuizQuestion[] };
export type QuizSummary = {
  questions: number;
  estimated_min: number;
  max_attempts: number;
  pass_percent: number;
  time_limit_s: number;
  remaining: number; // -1 = sin límite
  next_at: string | null;
  due_at: string | null;
  request_id: string;
  passed: boolean;
  in_progress: string;
};
export type QuizStatus = 'draft' | 'published' | 'archived';
export type QuizListed = { id: string; title: string; status: QuizStatus; manage: boolean; summary: QuizSummary | null };
export type Quiz = {
  id: string;
  title: string;
  status: QuizStatus;
  open_to_readers: boolean;
  page: { slug: string; title: string; level: Level };
  created_by: string;
  manage: boolean;
  results: boolean;
  summary: QuizSummary | null;
  // Solo para quien lo administra.
  draft?: QuizDraft;
  dirty?: boolean;
  page_changed?: boolean;
  published_version?: number;
};
export type QuestionView = {
  id: string;
  kind: QuizKind;
  prompt_html: string;
  points: number;
  options: { id: string; text: string }[];
  // Solo al corregir, según lo que el cuestionario deja ver.
  correct?: boolean;
  earned?: number;
  anchor?: string;
  correct_options?: string[];
  explanation_html?: string;
};
export type AttemptResult = {
  score: number;
  max: number;
  percent: number;
  passed: boolean;
  timed_out: boolean;
  feedback: QuizSettings['feedback'];
  revealed: boolean;
  submitted_at: string;
  duration_s: number;
};
export type AttemptView = {
  id: string;
  quiz_id: string;
  number: number;
  preview: boolean;
  status: 'in_progress' | 'submitted' | 'timed_out';
  started_at: string;
  deadline_at: string | null;
  pass_percent: number;
  questions: QuestionView[];
  answers: Record<string, string[]>;
  result?: AttemptResult;
};
export type QuizResults = {
  people: {
    user_id: string;
    name: string;
    email: string;
    attempts: number;
    percent: number;
    passed: boolean;
    in_progress: boolean;
    last_attempt_at: string | null;
    last_duration_s: number;
  }[];
  questions: { id: string; prompt: string; kind: QuizKind; answered: number; correct: number; top_wrong: string; top_wrong_count: number }[];
};

export type Sent = { id: string; area: { slug: string; name: string }; slug: string; status: ProposalStatus; comment: string; created_at: string };

// Vigencia de una página (§4.8). interval_days es el suyo (0 = el del área) y area_interval_days
// el del área (0 = no vence); due_at es null si no vence. can_review: quien lee es su dueño o un admin.
export type Review = {
  owner_id: string;
  owner: string;
  interval_days: number;
  area_interval_days: number;
  verified_at: string;
  verified_by: string;
  due_at: string | null;
  overdue: boolean;
  can_review: boolean;
};

// Una página de la cola de revisión (§4.8).
export type ReviewItem = { slug: string; title: string; level: Level; owner: string; verified_at: string; due_at: string };

export type WatchMode = '' | 'page' | 'subtree';

// Un aviso de la campana (§12.1). page es la ruta actual de la página (vacía si se borró).
export type NotificationKind =
  | 'page_changed'
  | 'page_deleted'
  | 'proposal_pending'
  | 'proposal_reminder'
  | 'proposal_approved'
  | 'proposal_rejected'
  | 'proposal_rebase'
  | 'review_due'
  | 'review_reminder'
  | 'read_request'
  | 'read_reminder'
  | 'quiz_assigned'
  | 'quiz_reminder'
  | 'quiz_extra_attempts'
  | 'quiz_failed_out';
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
export const notificationCategories = ['proposals', 'my_proposals', 'watched', 'review', 'reading', 'quiz_extra', 'quiz_failed'] as const;
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
  if (n.kind === 'review_reminder') return `${area}/review`;
  if (n.kind === 'quiz_failed_out') return `${area}/reads`;
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
// Plantilla (SPEC §4.9): la lista no trae markdown; al usarla, viene llena con las variables.
export type Template = { id: string; name: string; description: string; title: string; markdown?: string; level: Level; tags: string[]; review_interval_days: number };
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
export type Facets = { tags: Facet[]; folders: Facet[]; authors: Facet[]; dates: { days: number; count: number }[]; overdue: number };
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
// isPersonalSlug: la ruta de un espacio personal empieza con "~" (§7); un área no puede.
export const isPersonalSlug = (slug: string) => slug.startsWith('~');
// canWrite: puede abrir el editor, para publicar o para proponer (un lector, si el área lo deja).
export const canWrite = (a: Area) => canEdit(a) || a.viewers_can_propose;

// Puntaje de un cuestionario: 66,7 en español, 66.7 en inglés (llega redondeado a un decimal).
export const formatPercent = (n: number, locale: string) => new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(n);

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
