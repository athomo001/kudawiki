<script lang="ts">
  import { untrack } from 'svelte';
  import { api, ApiError, codeOf, formatDate, mediaMarkdown, timeAgo, type SealedDoc, type SealedGrant, type SealedRevision, type Upload } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';
  import { enhance } from '../lib/enhance';
  import { setDocumentTitle } from '../lib/site.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Icon from '../components/Icon.svelte';
  import NotFound from './NotFound.svelte';
  import Watermark from '../components/Watermark.svelte';

  // Un documento sellado (§6). Cerrado muestra solo su ruta y cómo abrirlo; abierto, el contenido
  // que el servidor descifra para esta sesión. Nada de esto se guarda en el navegador: ni
  // borradores ni autoguardado, y el servidor responde con no-store. Edita quien lo abrió con la
  // contraseña; con la llave maestra es solo lectura (§6.1, §6.2).
  // inArea: se ve dentro del área (con su árbol); fuera, p. ej. el superadmin, que no es miembro.
  // memberOf: las áreas de quien mira; si el sellado es de una de ellas, se abre dentro.
  let {
    id,
    inArea = false,
    memberOf = [],
    ondeleted,
  }: { id: string; inArea?: boolean; memberOf?: string[]; ondeleted?: () => void } = $props();

  let doc = $state<SealedDoc | null>(null);
  let missing = $state(false);
  let error = $state('');
  let adminError = $state(''); // el de abrir con la llave maestra va junto a ese botón
  let superError = $state(''); // el de aprobar un restablecimiento o desclasificar, junto a esos botones
  let declassifying = $state(false);
  let busy = $state(false);
  let password = $state('');
  let repeat = $state('');
  let revision = $state(0); // 0: la vigente
  let history = $state<SealedRevision[] | null>(null);
  let editing = $state(false);
  let draft = $state({ title: '', markdown: '' });
  let deleting = $state(false);
  // Accesos (§6.1): solo el autor y el superadmin los ven y los cambian.
  let grants = $state<SealedGrant[]>([]);
  let grantEmail = $state('');
  let grantPermission = $state<'read' | 'edit'>('read');
  let grantError = $state('');
  // Quien recibió un acceso abre con su clave de bóveda; si además conoce la contraseña, también.
  let usePassword = $state(false);
  let confirm = $state('');
  let prose = $state<HTMLElement>();
  // Dónde vive: el nombre del área, o de quién es el espacio personal (ese no tiene nombre).
  const place = $derived(!doc ? '' : !doc.area.personal ? doc.area.name : doc.is_author ? t('personal.title') : t('sealed.personal_of', { name: doc.author }));
  // La palabra la valida el servidor tal cual, en cualquier idioma (§6.1).
  const WORD = 'BORRAR';
  const old = $derived(!!doc && !doc.locked && doc.revision !== doc.current_revision);

  async function load() {
    error = '';
    try {
      doc = await api<SealedDoc>('GET', `/sealed/${id}${revision ? `?revision=${revision}` : ''}`);
      if (doc.can_grant && !doc.locked) grants = await api<SealedGrant[]>('GET', `/sealed/${id}/grants`);
      if (!inArea && memberOf.includes(doc.area.slug)) navigate(`/w/${doc.area.slug}/sealed/${id}`, { replace: true });
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) missing = true;
      else error = errorText(codeOf(e));
    }
  }

  $effect(() => {
    void id;
    doc = null;
    missing = editing = false;
    history = null;
    revision = 0;
    untrack(load); // load lee revision: si la siguiera, elegir otra revisión volvería a la vigente
  });

  $effect(() => {
    void locale();
    if (doc?.html && prose) enhance(prose);
  });

  // El título del documento va cifrado: la pestaña lleva solo la ruta, que ya se sabe.
  $effect(() => {
    if (doc) setDocumentTitle(doc.slug);
    return () => setDocumentTitle();
  });

  // Pasados 15 minutos sin usarlo el servidor lo cierra: cualquier pedido responde 423 y se
  // vuelve a la pantalla de abrir, sin perder lo que se estaba escribiendo en el formulario.
  async function run(fn: () => Promise<void>, fail = (m: string) => (error = m)) {
    busy = true;
    error = adminError = superError = grantError = '';
    try {
      await fn();
    } catch (e) {
      if (e instanceof ApiError && e.status === 423) {
        if (doc) doc.locked = true;
        error = t('sealed.relocked');
      } else fail(errorText(codeOf(e)));
    } finally {
      busy = false;
    }
  }

  function unlock(e: SubmitEvent) {
    e.preventDefault();
    if (doc?.needs_password && password !== repeat) {
      error = t('sealed.password_mismatch');
      return;
    }
    const vault = !!doc?.grant && !usePassword;
    run(async () => {
      await api('POST', `/sealed/${id}/unlock`, vault ? { vault_key: password } : { password });
      password = repeat = '';
      await load();
    });
  }

  function openAsAdmin() {
    run(
      async () => {
        await api('POST', `/sealed/${id}/unlock`, { as_admin: true });
        await load();
      },
      (m) => (adminError = m),
    );
  }

  // Restablecer la contraseña (§6.3): el autor pide, el superadmin aprueba y el autor pone la
  // nueva. Aprobar y ponerla piden confirmar quién eres (api() abre el diálogo).
  function requestReset() {
    run(async () => {
      await api('POST', `/sealed/${id}/password-reset`);
      await load();
    });
  }

  function approveReset() {
    run(
      async () => {
        await api('POST', `/sealed/${id}/password-reset/approve`);
        await load();
      },
      (m) => (superError = m),
    );
  }

  function completeReset(e: SubmitEvent) {
    e.preventDefault();
    if (password !== repeat) {
      error = t('sealed.password_mismatch');
      return;
    }
    run(async () => {
      await api('POST', `/sealed/${id}/password-reset/complete`, { new_password: password });
      password = repeat = '';
      await load();
    });
  }

  // Desclasificar (§6.1): queda una página del área. El superadmin que no es del área no la puede
  // ver, así que vuelve a la lista de sellados.
  function declassify(e: SubmitEvent) {
    e.preventDefault();
    run(
      async () => {
        const to = await api<{ area: string; slug: string }>('POST', `/sealed/${id}/declassify`);
        declassifying = false;
        doc = null;
        ondeleted?.();
        navigate(inArea || memberOf.includes(to.area) ? `/w/${to.area}/p/${to.slug}` : '/admin/sealed');
      },
      (m) => (superError = m),
    );
  }

  function showRevision(n: number) {
    revision = n === doc?.current_revision ? 0 : n;
    run(load);
  }

  function toggleHistory() {
    if (history) {
      history = null;
      return;
    }
    run(async () => {
      history = await api<SealedRevision[]>('GET', `/sealed/${id}/history`);
    });
  }

  function startEdit() {
    if (!doc) return;
    draft = { title: doc.title ?? '', markdown: doc.markdown ?? '' };
    editing = true;
  }

  // Adjuntar (F7.6): el archivo se guarda cifrado con la DEK del documento y su enlace se escribe
  // donde está el cursor. Solo sirve con el documento abierto, como el resto.
  let content = $state<HTMLTextAreaElement>();
  let picker = $state<HTMLInputElement>();
  let attaching = $state(false);
  async function attach(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const files = [...(input.files ?? [])];
    input.value = '';
    error = '';
    attaching = true;
    for (const file of files) {
      try {
        const body = new FormData();
        body.append('file', file);
        const m = await api<Upload>('POST', `/sealed/${id}/files`, body);
        const at = content?.selectionStart ?? draft.markdown.length;
        const text = `${mediaMarkdown(m)}
`;
        draft.markdown = draft.markdown.slice(0, at) + text + draft.markdown.slice(content?.selectionEnd ?? at);
      } catch (err) {
        error = errorText(codeOf(err));
      }
    }
    attaching = false;
  }

  function save(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      await api('PUT', `/sealed/${id}`, { ...draft, base_revision: doc?.current_revision });
      editing = false;
      history = null;
      await load();
    });
  }

  function addGrant(e: SubmitEvent) {
    e.preventDefault();
    run(
      async () => {
        await api('POST', `/sealed/${id}/grants`, { email: grantEmail.trim(), permission: grantPermission });
        grantEmail = '';
        grants = await api<SealedGrant[]>('GET', `/sealed/${id}/grants`);
      },
      (m) => (grantError = m),
    );
  }

  function revoke(userID: string) {
    run(
      async () => {
        await api('DELETE', `/sealed/${id}/grants/${userID}`);
        grants = grants.filter((g) => g.user_id !== userID);
      },
      (m) => (grantError = m),
    );
  }

  function remove(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      await api('DELETE', `/sealed/${id}`, { confirm });
      const to = inArea && doc ? `/w/${doc.area.slug}` : '/';
      deleting = false;
      doc = null;
      ondeleted?.();
      navigate(to);
    });
  }
</script>

{#snippet superActions()}
  <div class="sealed-admin">
    <h2 class="side-title">{t('sealed.super_title')}</h2>
    {#if superError && !declassifying}<p class="error" role="alert">{superError}</p>{/if}
    {#if doc?.has_password}
      {#if doc.reset === 'approved'}
        <p class="muted small">{t('sealed.super_reset_approved')}</p>
      {:else}
        {#if doc.reset === 'requested'}<p>{t('sealed.super_reset_requested')}</p>{/if}
        <p class="muted small">{t('sealed.reset_approve_hint')}</p>
        <button type="button" disabled={busy} onclick={approveReset}>{t('sealed.reset_approve')}</button>
      {/if}
    {/if}
    <p class="muted small">{t('sealed.declassify_hint')}</p>
    <button type="button" class="danger" disabled={busy} onclick={() => ((superError = ''), (declassifying = true))}>{t('sealed.declassify')}</button>
  </div>
{/snippet}

{#if missing}
  <NotFound />
{:else if doc?.locked}
  <section class={['sealed-lock', inArea && 'view']} aria-labelledby="sealed-lock-title">
    <p class="article-badges"><Icon name="lock" size={14} /> {t('sealed.badge')}{#if !inArea} · {place}{/if}</p>
    <h1 id="sealed-lock-title">{doc.slug}</h1>
    <p class="muted">{t('sealed.author', { name: doc.author })}</p>
    {#if doc.needs_password}
      <p>{t('sealed.received')}</p>
      <form class="stack sealed-form" onsubmit={unlock}>
        <div class="sealed-passwords">
          <label class="field">{t('sealed.password_new')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={password} /></label>
          <label class="field">{t('sealed.password_repeat')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={repeat} /></label>
        </div>
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <button type="submit" class="primary" disabled={busy}>{t('sealed.set_password')}</button>
      </form>
    {:else if doc.grant && !usePassword && !doc.has_vault}
      <p>{t('sealed.needs_vault')} <a href="/me/profile#vault-title">{t('vault.title')}</a></p>
    {:else if doc.is_author && doc.reset === 'approved'}
      <p>{t('sealed.reset_approved')}</p>
      <form class="stack sealed-form" onsubmit={completeReset}>
        <div class="sealed-passwords">
          <label class="field">{t('sealed.password_new')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={password} /></label>
          <label class="field">{t('sealed.password_repeat')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={repeat} /></label>
        </div>
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <button type="submit" class="primary" disabled={busy}>{t('sealed.reset_set')}</button>
      </form>
    {:else}
      <form class="stack sealed-form" onsubmit={unlock}>
        <label class="field">
          {t(doc.grant && !usePassword ? 'vault.key' : 'sealed.password')}
          <input type="password" required autocomplete="off" bind:value={password} />
        </label>
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <button type="submit" class="primary" disabled={busy}>{t('sealed.unlock')}</button>
      </form>
    {/if}
    {#if doc.grant && !doc.needs_password}
      <p>
        <button type="button" class="link" onclick={() => ((usePassword = !usePassword), (password = ''), (error = ''))}>
          {t(usePassword ? 'sealed.use_vault' : 'sealed.use_password')}
        </button>
      </p>
    {/if}
    {#if doc.is_author && doc.has_password && doc.reset !== 'approved'}
      <div class="sealed-admin">
        {#if doc.reset === 'requested'}
          <p class="muted small">{t('sealed.reset_requested')}</p>
        {:else}
          <h2 class="side-title">{t('sealed.forgot')}</h2>
          <p class="muted small">{t('sealed.reset_request_hint')}</p>
          <button type="button" disabled={busy} onclick={requestReset}>{t('sealed.reset_request')}</button>
        {/if}
      </div>
    {/if}
    {#if doc.can_admin_open && !doc.is_author}
      <div class="sealed-admin">
        <h2 class="side-title">{t('sealed.admin_title')}</h2>
        <p class="muted small">{t('sealed.admin_hint')}</p>
        {#if adminError}<p class="error" role="alert">{adminError}</p>{/if}
        <button type="button" disabled={busy} onclick={openAsAdmin}>{t('sealed.admin_open')}</button>
      </div>
    {/if}
    {#if doc.can_declassify}{@render superActions()}{/if}
    {#if doc.can_delete}
      <p><button type="button" class="link danger" onclick={() => ((confirm = ''), (deleting = true))}>{t('sealed.delete')}</button></p>
    {/if}
  </section>
{:else if doc && editing}
  <!-- Un sellado lleva marca de agua siempre que su contenido está a la vista (§4.15). -->
  <Watermark />
  <form class={['stack sealed-form sealed-editor', inArea && 'view']} onsubmit={save}>
    <p class="article-badges"><Icon name="lock" size={14} /> {t('sealed.badge')} · {doc.slug}</p>
    <label class="field">{t('page.title')} <input required maxlength="200" bind:value={draft.title} /></label>
    <label class="field">{t('sealed.content')} <textarea rows="20" bind:this={content} bind:value={draft.markdown}></textarea></label>
    <div class="sealed-attach">
      <button type="button" disabled={attaching} onclick={() => picker?.click()}><Icon name="image" size={16} /> {attaching ? t('sealed.attaching') : t('sealed.attach')}</button>
      <small class="muted">{t('sealed.attach_hint')}</small>
      <input class="sr-only" type="file" multiple tabindex="-1" aria-hidden="true" bind:this={picker} onchange={attach} />
    </div>
    <p class="muted small">{t('sealed.no_drafts')}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="form-actions">
      <button type="submit" class="primary" disabled={busy}>{busy ? t('common.saving') : t('common.save')}</button>
      <button type="button" class="link" onclick={() => (editing = false)}>{t('common.cancel')}</button>
    </div>
  </form>
{:else if doc}
  <div class="reader">
    <Watermark />
    <article class="article">
      <p class="article-badges"><Icon name="lock" size={14} /> {t('sealed.badge')}{#if !doc.can_edit} · {t('sealed.read_only')}{/if}{#if !inArea} · {place}{/if}</p>
      <h1>{doc.title}</h1>
      {#if old}
        <p class="notice" role="note">
          {t('sealed.old_revision', { n: String(doc.revision) })}
          <button type="button" class="link" onclick={() => showRevision(0)}>{t('sealed.back_current')}</button>
        </p>
      {/if}
      <hr />
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <!-- HTML saneado por bluemonday en el servidor (SPEC §4.1), como en el lector. -->
      <div class="prose" bind:this={prose}>{@html doc.html}</div>
    </article>
    <aside class="page-info" aria-label={t('page.info')}>
      {#if doc.toc?.length}
        <nav aria-label={t('page.toc')}>
          <h2 class="side-title">{t('page.toc')}</h2>
          <ul>
            {#each doc.toc as h (h.id)}
              <li class:sub={h.level === 3}><a href={`#${h.id}`}>{h.text}</a></li>
            {/each}
          </ul>
        </nav>
      {/if}
      <p class="page-meta">
        <span>{doc.slug}</span>
        <span>{t('sealed.author', { name: doc.author })}</span>
        {#if doc.updated_at}
          <span>
            {t('page.edited_by', { name: doc.updated_by ?? '' })}
            <time datetime={doc.updated_at} title={formatDate(doc.updated_at, locale())}>{timeAgo(doc.updated_at, locale())}</time>
          </span>
        {/if}
      </p>
      <div class="sealed-actions">
        {#if doc.can_edit && !old}<button type="button" class="link" onclick={startEdit}><Icon name="edit" size={14} /> {t('page.edit')}</button>{/if}
        <button type="button" class="link" aria-expanded={!!history} onclick={toggleHistory}><Icon name="history" size={14} /> {t('page.history')}</button>
        {#if doc.can_delete}<button type="button" class="link danger" onclick={() => ((confirm = ''), (deleting = true))}><Icon name="trash" size={14} /> {t('sealed.delete')}</button>{/if}
      </div>
      {#if doc.can_grant}
        <section class="sealed-grants" aria-labelledby="sealed-grants-title">
          <h2 id="sealed-grants-title" class="side-title">{t('sealed.grants')}</h2>
          {#if grants.length}
            <ul>
              {#each grants as g (g.user_id)}
                <li>
                  <span>{g.full_name} <small class="muted">· {t(`sealed.access_${g.permission}`)}</small></span>
                  <button type="button" class="link danger" disabled={busy} onclick={() => revoke(g.user_id)}>{t('sealed.revoke')}</button>
                </li>
              {/each}
            </ul>
          {:else}
            <p class="muted small">{t('sealed.grants_none')}</p>
          {/if}
          <form class="stack" onsubmit={addGrant}>
            <label class="field">{t('sealed.grant_email')} <input type="email" required autocomplete="off" bind:value={grantEmail} /></label>
            <label class="field">
              {t('sealed.grant_permission')}
              <select bind:value={grantPermission}>
                <option value="read">{t('sealed.access_read')}</option>
                <option value="edit">{t('sealed.access_edit')}</option>
              </select>
            </label>
            {#if grantError}<p class="error" role="alert">{grantError}</p>{/if}
            <button type="submit" disabled={busy}>{t('sealed.grant')}</button>
          </form>
        </section>
      {/if}
      {#if doc.can_declassify}{@render superActions()}{/if}
      {#if history}
        <ol class="sealed-history">
          {#each history as h (h.revision)}
            <li>
              <button type="button" class="link" aria-current={h.revision === doc.revision ? 'true' : undefined} onclick={() => showRevision(h.revision)}>
                {t('sealed.revision', { n: String(h.revision) })}
              </button>
              <small class="muted">{h.author} · <time datetime={h.created_at} title={formatDate(h.created_at, locale())}>{timeAgo(h.created_at, locale())}</time></small>
            </li>
          {/each}
        </ol>
      {/if}
    </aside>
  </div>
{:else if error}
  <p class="error" role="alert">{error}</p>
{/if}

<Dialog title={t('sealed.delete_title')} bind:open={deleting}>
  <form class="stack" onsubmit={remove}>
    <p>{t('sealed.delete_warning', { slug: doc?.slug ?? '' })}</p>
    <label class="field">{t('sealed.delete_type', { word: WORD })} <input required autocomplete="off" bind:value={confirm} /></label>
    {#if error && deleting}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary danger" disabled={busy || confirm.trim() !== WORD}>{t('sealed.delete_confirm')}</button>
  </form>
</Dialog>

<Dialog title={t('sealed.declassify_title')} bind:open={declassifying}>
  <form class="stack" onsubmit={declassify}>
    <p>{t('sealed.declassify_warning', { slug: doc?.slug ?? '', place })}</p>
    {#if superError && declassifying}<p class="error" role="alert">{superError}</p>{/if}
    <button type="submit" class="primary danger" disabled={busy}>{t('sealed.declassify_confirm')}</button>
  </form>
</Dialog>
