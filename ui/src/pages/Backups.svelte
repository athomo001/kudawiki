<script lang="ts">
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';
  import Dialog from '../components/Dialog.svelte';

  // Administración → Respaldos (SPEC §13.3, F8-B): el respaldo diario (siempre completo), el destino
  // externo, la lista de lo que hay en la carpeta, "Respaldar ahora", cómo restaurar (por consola,
  // con el servidor detenido) y "Borrar todo". La clave del destino viene enmascarada, como la del
  // correo (§1). Respaldar y borrar corren en el servidor: la pantalla pregunta el estado hasta que
  // terminan. Molde del diseño "Kudawiki — Administración" (§15).

  type Remote = {
    endpoint: string;
    region: string;
    bucket: string;
    prefix: string;
    access_key_id: string;
    secret: string;
    path_style: boolean;
  };
  type Settings = {
    time: string;
    keep_daily: number;
    keep_weekly: number;
    keep_monthly: number;
    remote: Remote;
    locked: string[]; // fijadas en kudawiki.yaml o KUDAWIKI_BACKUP_*: el panel no las cambia (§13.1)
    dir: string;
    has_key: boolean;
  };
  type Kind = 'daily' | 'manual' | 'pre-migrate' | 'pre-rotate' | 'pre-reset';
  // protected: el último probado y los de antes de "Borrar todo" no se borran desde aquí (F8-B.6).
  type Entry = { name: string; kind: Kind; created_at: string; size: number; uploaded: boolean; verified: boolean; protected: boolean };
  type Result = { kind: 'backup' | 'reset'; at: string; file?: string; ok: boolean; error?: string };
  type State = { settings: Settings; backups: Entry[]; running: '' | 'backup' | 'reset'; last: Result | null };

  // El texto exacto que pide el servidor, igual en los dos idiomas (backups.ConfirmPhrase).
  const phrase = 'BORRAR TODO';
  const remoteKeys = ['endpoint', 'region', 'bucket', 'prefix', 'access_key_id', 'secret', 'path_style'] as const;
  const scheduleKeys = ['time', 'keep_daily', 'keep_weekly', 'keep_monthly'] as const;

  let st = $state<State | null>(null);
  let form = $state<Settings | null>(null);
  let saved = $state('');
  let original = '';
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let confirm = $state('');
  let resetError = $state('');
  let restoring = $state<Entry | null>(null);
  let restoreOpen = $state(false);
  let deleting = $state<Entry | null>(null);
  let deleteOpen = $state(false);
  let deleteBusy = $state(false);
  let deleteError = $state('');
  // Lo último que se pidió desde esta pantalla: al terminar se avisa cómo salió.
  let waiting = $state<'' | 'backup' | 'reset'>('');

  const locked = (key: string) => form?.locked.includes(key) ?? false;
  // Lo que el panel puede mandar: una clave fijada en el archivo no se manda, aunque no cambie.
  function editable(f: Settings) {
    const out: Record<string, unknown> = {};
    for (const k of scheduleKeys) if (!locked(k)) out[k] = f[k];
    const remote: Record<string, unknown> = {};
    for (const k of remoteKeys) {
      const fileKey = k === 'secret' ? 'remote.secret_access_key_file' : `remote.${k}`;
      if (!locked(fileKey)) remote[k] = f.remote[k];
    }
    out.remote = remote;
    return out;
  }
  const dirty = $derived(!!form && JSON.stringify(editable(form)) !== saved);
  const changes = $derived(form && saved ? countChanges(JSON.parse(saved), editable(form)) : 0);
  const running = $derived(st?.running ?? '');

  function showSettings(s: Settings) {
    original = JSON.stringify(s);
    form = s;
    saved = JSON.stringify(editable(s));
  }

  async function load() {
    const next = await api<State>('GET', '/admin/backups');
    // La configuración solo se pisa si no hay cambios a medio hacer.
    if (!form || !dirty) showSettings(next.settings);
    st = next;
    if (waiting && !next.running) finished(next.last);
  }

  function finished(r: Result | null) {
    const kind = waiting;
    waiting = '';
    if (!r || r.kind !== kind) return;
    if (r.kind === 'backup') {
      if (r.ok) notice = t('backups.run_done', { file: r.file ?? '' });
      else error = t('backups.run_failed');
    } else if (r.ok) {
      // Después de borrar, la sesión de quien lo hizo sigue, pero todo lo demás cambió: se recarga.
      location.assign('/admin/backups?reset=1');
    } else {
      resetError = t('backups.reset_failed');
    }
  }

  load().catch((e) => (error = errorText(codeOf(e))));
  if (new URLSearchParams(location.search).has('reset')) notice = t('backups.reset_done');

  // Mientras algo corre, se pregunta cada 2 s; si no, no hay nada que mirar.
  $effect(() => {
    if (!running) return;
    const id = setInterval(() => load().catch((e) => (error = errorText(codeOf(e)))), 2000);
    return () => clearInterval(id);
  });

  async function save() {
    if (!form) return;
    busy = true;
    error = '';
    notice = '';
    try {
      showSettings(await api<Settings>('PUT', '/admin/backups/settings', editable(form)));
      notice = t('backups.saved');
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  // Borrar un respaldo pide confirmar quién eres: api() abre el diálogo de reautenticación.
  async function remove() {
    if (!deleting) return;
    deleteBusy = true;
    deleteError = '';
    try {
      await api('DELETE', `/admin/backups/${encodeURIComponent(deleting.name)}`);
      deleteOpen = false;
      error = '';
      notice = t('backups.deleted', { file: deleting.name });
      await load();
    } catch (err) {
      deleteError = errorText(codeOf(err));
    } finally {
      deleteBusy = false;
    }
  }

  // Bajar un respaldo pide confirmar quién eres (F8-B.7). Un enlace no puede abrir ese diálogo:
  // primero se pide el respaldo por api(), que lo abre si hace falta, y después se navega a la
  // descarga, que el navegador guarda como archivo sin cargarlo en memoria.
  async function download(b: Entry) {
    error = '';
    const path = `/admin/backups/${encodeURIComponent(b.name)}`;
    try {
      await api('GET', path);
      location.assign(`/api${path}/download`);
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }

  async function start(kind: 'backup' | 'reset') {
    error = notice = resetError = '';
    try {
      if (kind === 'backup') await api('POST', '/admin/backups');
      else await api('POST', '/admin/reset', { confirm });
      waiting = kind;
      await load();
    } catch (err) {
      if (kind === 'backup') error = errorText(codeOf(err));
      else resetError = errorText(codeOf(err));
    }
  }

  const size = (n: number) =>
    n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`;
  const path = (e: Entry) => `${st?.settings.dir ?? ''}/${e.name}`;
</script>

{#snippet hint(key: string, text: string)}
  <small class="muted">{locked(key) ? t('appearance.locked') : text}</small>
{/snippet}

<PageHead context={`${t('nav.admin')} · ${t('admin.group_site')}`} title={t('backups.title')} description={t('backups.intro')}>
  {#snippet status()}
    {#if running}<span class="pill warn">{t(`backups.running_${running}`)}</span>{/if}
  {/snippet}
  {#snippet actions()}
    <button type="button" class="primary" disabled={!st || !!running} onclick={() => start('backup')}>
      {running === 'backup' ? t('backups.running_backup') : t('backups.run')}
    </button>
  {/snippet}
</PageHead>

{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if notice && !dirty}<p class="notice" role="status">{notice}</p>{/if}

{#if form && st}
  <form
    onsubmit={(e) => {
      e.preventDefault();
      save();
    }}
  >
    <Setting title={t('backups.schedule')} description={t('backups.schedule_desc')}>
      <label class="field">
        {t('backups.time')}
        <input type="time" required disabled={locked('time')} bind:value={form.time} />
        {@render hint('time', t('backups.time_hint'))}
      </label>
      <fieldset class="field">
        <legend>{t('backups.keep')}</legend>
        <div class="inline">
          <label>{t('backups.keep_daily')} <input type="number" class="port" min="1" max="3650" disabled={locked('keep_daily')} bind:value={form.keep_daily} /></label>
          <label>{t('backups.keep_weekly')} <input type="number" class="port" min="0" max="520" disabled={locked('keep_weekly')} bind:value={form.keep_weekly} /></label>
          <label>{t('backups.keep_monthly')} <input type="number" class="port" min="0" max="240" disabled={locked('keep_monthly')} bind:value={form.keep_monthly} /></label>
        </div>
        <small class="muted">{t('backups.keep_hint')}</small>
      </fieldset>
      <div class="field">
        {t('backups.dir')}
        <code class="mono">{form.dir}</code>
        <small class="muted">{t('backups.dir_hint')}</small>
      </div>
    </Setting>

    <Setting title={t('backups.remote')} description={t('backups.remote_desc')}>
      {#if !form.has_key}<p class="callout" role="note">{t('backups.no_key')} <code>kudawiki backup keygen</code></p>{/if}
      <label class="field">
        {t('backups.endpoint')}
        <input maxlength="512" spellcheck="false" autocomplete="off" placeholder="https://s3.empresa.cl" disabled={locked('remote.endpoint')} bind:value={form.remote.endpoint} />
        {@render hint('remote.endpoint', t('backups.endpoint_hint'))}
      </label>
      <label class="field">
        {t('backups.bucket')}
        <input maxlength="512" spellcheck="false" autocomplete="off" disabled={locked('remote.bucket')} bind:value={form.remote.bucket} />
        {@render hint('remote.bucket', '')}
      </label>
      <label class="field">
        {t('backups.prefix')}
        <input maxlength="512" spellcheck="false" autocomplete="off" placeholder="kudawiki/" disabled={locked('remote.prefix')} bind:value={form.remote.prefix} />
        {@render hint('remote.prefix', t('backups.prefix_hint'))}
      </label>
      <label class="field">
        {t('backups.region')}
        <input maxlength="512" spellcheck="false" autocomplete="off" disabled={locked('remote.region')} bind:value={form.remote.region} />
        {@render hint('remote.region', '')}
      </label>
      <label class="field">
        {t('backups.access_key_id')}
        <input maxlength="512" spellcheck="false" autocomplete="off" disabled={locked('remote.access_key_id')} bind:value={form.remote.access_key_id} />
        {@render hint('remote.access_key_id', '')}
      </label>
      <label class="field">
        {t('backups.secret')}
        <input type="password" maxlength="512" autocomplete="new-password" disabled={locked('remote.secret_access_key_file')} bind:value={form.remote.secret} />
        {@render hint('remote.secret_access_key_file', t('backups.secret_hint'))}
      </label>
      <label class="choice">
        <input type="checkbox" disabled={locked('remote.path_style')} bind:checked={form.remote.path_style} />
        <span><strong>{t('backups.path_style')}</strong><small>{t('backups.path_style_hint')}</small></span>
      </label>
    </Setting>
  </form>

  <h2 class="section-title">{t('backups.list')}</h2>
  <p class="muted small">{t('backups.list_hint')}</p>
  <div class="table-scroll wide-table">
    <table class="admin-table">
      <thead>
        <tr>
          <th scope="col">{t('backups.col_date')}</th>
          <th scope="col">{t('backups.col_kind')}</th>
          <th scope="col" class="num">{t('backups.col_size')}</th>
          <th scope="col">{t('backups.col_state')}</th>
          <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
        </tr>
      </thead>
      <tbody>
        {#each st.backups as b (b.name)}
          <tr>
            <td>{formatDate(b.created_at, locale())}<br /><small class="muted mono">{b.name}</small></td>
            <td>{t(`backups.kind.${b.kind}`)}</td>
            <td class="num">{size(b.size)}</td>
            <td>
              {#if b.uploaded}<span class="pill ok">{t('backups.uploaded')}</span>{/if}
              {#if b.verified}<span class="pill ok">{t('backups.verified')}</span>{/if}
            </td>
            <td class="row-actions backup-actions">
              {#if !b.protected}
                <button
                  type="button"
                  class="link danger"
                  disabled={!!running}
                  aria-label={t('backups.delete_label', { file: b.name })}
                  onclick={() => ((deleting = b), (deleteError = ''), (deleteOpen = true))}>{t('backups.delete')}</button
                >
              {/if}
              <button type="button" class="link" aria-label={t('backups.download_label', { file: b.name })} onclick={() => download(b)}
                >{t('backups.download')}</button
              >
              <button type="button" class="secondary" onclick={() => ((restoring = b), (restoreOpen = true))}>{t('backups.restore')}</button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="5" class="muted">{t('backups.none')}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>

  <Setting title={t('backups.reset')} description={t('backups.reset_desc')}>
    <form
      class="form-actions"
      onsubmit={(e) => {
        e.preventDefault();
        start('reset');
      }}
    >
      <label class="field">{t('backups.reset_confirm', { phrase })} <input class="mono" autocomplete="off" bind:value={confirm} /></label>
      <button type="submit" class="danger-btn" disabled={confirm !== phrase || !!running}>
        {running === 'reset' ? t('backups.running_reset') : t('backups.reset_apply')}
      </button>
    </form>
    {#if resetError}<p class="error" role="alert">{resetError}</p>{/if}
  </Setting>

  <SaveBar count={changes} {busy} onsave={() => save()} ondiscard={() => showSettings(JSON.parse(original))} />
{/if}

<Dialog title={t('backups.restore_title')} bind:open={restoreOpen}>
  {#if restoring}
    <p>{t('backups.restore_intro')}</p>
    <pre class="smtp-detail"><code>kudawiki restore {path(restoring)}</code></pre>
    <p>{t('backups.restore_docker')}</p>
    <pre class="smtp-detail"><code>docker compose stop kudawiki
docker compose run --rm kudawiki restore {path(restoring)}
docker compose up -d</code></pre>
    <p class="muted">{t('backups.restore_after')}</p>
  {/if}
</Dialog>

<Dialog title={t('backups.delete_title')} bind:open={deleteOpen}>
  {#if deleting}
    <p><strong>{formatDate(deleting.created_at, locale())}</strong> · {t(`backups.kind.${deleting.kind}`)} · {size(deleting.size)}<br /><small class="muted mono">{deleting.name}</small></p>
    <p>{t('backups.delete_intro')}</p>
    {#if deleteError}<p class="error" role="alert">{deleteError}</p>{/if}
    <div class="form-actions">
      <button type="button" class="secondary" onclick={() => (deleteOpen = false)}>{t('common.cancel')}</button>
      <button type="button" class="danger-btn" disabled={deleteBusy} onclick={remove}>{t('backups.delete_apply')}</button>
    </div>
  {/if}
</Dialog>
