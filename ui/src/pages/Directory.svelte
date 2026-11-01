<script lang="ts">
  import { api, codeOf, ApiError } from '../lib/api';
  import { t, has, errorText, type Key } from '../lib/i18n/index.svelte';
  import Dialog from '../components/Dialog.svelte';

  // Administración → Directorio (SPEC §8.6.4, §8.6.5): la conexión a LDAP / Active Directory y
  // su diagnóstico. Las pruebas usan lo que está en el formulario, sin guardarlo. La contraseña
  // de la cuenta de servicio nunca llega al navegador: viene enmascarada y la máscara de vuelta
  // es "sin cambios".

  type Flavor = 'ad' | 'openldap' | 'generic';
  type Attrs = { email: string; name: string; guid: string };
  type Defaults = { user_filter: string; login_attrs: string[]; attrs: Attrs; disabled_filter: string };
  type Settings = {
    enabled: boolean;
    flavor: Flavor;
    urls: string[];
    start_tls: boolean;
    ca_file: string;
    insecure_skip_verify: boolean;
    bind_dn: string;
    bind_password: string;
    domain_netbios: string;
    user_base_dn: string;
    user_filter: string;
    login_attrs: string[];
    group_base_dn: string;
    attrs: Attrs;
    disabled_filter: string;
    timeout: string;
    locked: string[]; // fijadas en kudawiki.yaml o KUDAWIKI_AUTH_LDAP_*: el panel no las cambia
    defaults: Record<Flavor, Defaults>;
  };
  type Step = { name: string; status: 'ok' | 'warn' | 'fail'; code?: string; detail?: string };
  type Cert = { subject: string; issuer: string; not_after: string; days_left: number; tls_version: string };
  type Server = { url: string; steps: Step[]; cert?: Cert; users?: number };
  type Report = { ok: boolean; servers: Server[]; suggestion?: { flavor: Flavor; guid: string; vendor?: string } };
  type UserReport = {
    url: string;
    login: string;
    filter: string;
    status: 'found' | 'not_found' | 'ambiguous';
    dn?: string;
    dns?: string[];
    enabled: boolean;
    attributes: string[];
    email: string;
    name: string;
    guid: string;
    missing: string[];
    warnings: string[];
  };

  const flavors: Flavor[] = ['ad', 'openldap', 'generic'];
  const scalars = [
    'enabled',
    'flavor',
    'start_tls',
    'ca_file',
    'insecure_skip_verify',
    'bind_dn',
    'bind_password',
    'domain_netbios',
    'user_base_dn',
    'user_filter',
    'group_base_dn',
    'disabled_filter',
    'timeout',
  ] as const;
  const attrKeys = ['email', 'name', 'guid'] as const;

  let form = $state<Settings | null>(null);
  // Las listas se editan como texto: una URL por línea, los atributos separados por comas.
  let urlsText = $state('');
  let loginAttrsText = $state('');
  let saved = $state('');
  let busy = $state(false);
  let error = $state('');
  let errorField = $state('');
  let notice = $state('');
  let report = $state<Report | null>(null);
  let testing = $state(false);
  let failedSave = $state(false); // la última vez que se guardó, la prueba falló: se ofrece "Guardar igual"
  let confirmForce = $state(false);
  let login = $state('');
  let userReport = $state<UserReport | null>(null);
  let userError = $state('');
  let userDetail = $state('');
  let lookingUp = $state(false);

  const locked = (key: string) => form?.locked.includes(key) ?? false;
  const defaults = $derived(form ? form.defaults[form.flavor] : null);
  const lines = (s: string) => s.split(/[\n,]/).map((v) => v.trim()).filter(Boolean);

  // editable es lo que se manda: una clave fijada en el archivo no va, porque el servidor la
  // rechazaría aunque no cambie.
  function editable(): Record<string, unknown> {
    if (!form) return {};
    const out: Record<string, unknown> = {};
    for (const k of scalars) if (!locked(k)) out[k] = form[k];
    if (!locked('urls')) out.urls = lines(urlsText);
    if (!locked('login_attrs')) out.login_attrs = lines(loginAttrsText);
    out.attrs = Object.fromEntries(attrKeys.filter((k) => !locked(`attrs.${k}`)).map((k) => [k, form!.attrs[k]]));
    return out;
  }
  const dirty = $derived(form !== null && JSON.stringify(editable()) !== saved);

  function show(s: Settings) {
    form = s;
    urlsText = s.urls.join('\n');
    loginAttrsText = s.login_attrs.join(', ');
    saved = JSON.stringify(editable());
  }

  api<Settings>('GET', '/admin/ldap/config')
    .then(show)
    .catch((e) => (error = errorText(codeOf(e))));

  function fail(err: unknown) {
    error = errorText(codeOf(err));
    errorField = err instanceof ApiError && typeof err.detail.field === 'string' ? err.detail.field : '';
  }

  async function save(force: boolean) {
    busy = true;
    error = '';
    errorField = '';
    notice = '';
    confirmForce = false;
    try {
      show(await api<Settings>('PUT', '/admin/ldap/config', { ...editable(), force }));
      notice = t('ldap.saved');
      failedSave = false;
    } catch (err) {
      fail(err);
      // No se guarda una configuración activa que no conecta: se muestra por qué y se ofrece
      // "Guardar igual" (§8.6.4).
      if (err instanceof ApiError && err.code === 'ldap_test_failed') {
        report = err.detail.test as Report;
        failedSave = true;
      }
    } finally {
      busy = false;
    }
  }

  async function testConnection() {
    testing = true;
    error = '';
    errorField = '';
    report = null;
    try {
      report = await api<Report>('POST', '/admin/ldap/test', { kind: 'connection', config: editable() });
    } catch (err) {
      fail(err);
    } finally {
      testing = false;
    }
  }

  async function testUser(e: SubmitEvent) {
    e.preventDefault();
    lookingUp = true;
    userError = '';
    userDetail = '';
    userReport = null;
    try {
      userReport = await api<UserReport>('POST', '/admin/ldap/test', { kind: 'user', login, config: editable() });
    } catch (err) {
      userError = errorText(codeOf(err));
      if (err instanceof ApiError && typeof err.detail.detail === 'string') userDetail = err.detail.detail;
      if (err instanceof ApiError && typeof err.detail.field === 'string' && err.detail.field !== 'login') {
        userError = `${userError} ${fieldLabel(err.detail.field)}`;
      }
    } finally {
      lookingUp = false;
    }
  }

  // Lo que sugirió el Root DSE: el superadmin lo confirma (§8.6.1).
  function useSuggestion() {
    if (!form || !report?.suggestion) return;
    if (!locked('flavor')) form.flavor = report.suggestion.flavor;
    if (!locked('attrs.guid')) form.attrs.guid = report.suggestion.guid;
  }

  function fieldLabel(field: string): string {
    const key = `ldap.${field.replace('.', '_')}`;
    return has(key) ? t(key) : field;
  }
  const codeText = (code?: string) => (code && has(`ldap.code.${code}`) ? t(`ldap.code.${code}` as Key) : '');
  const statusText = (s: Step['status']) => t(`ldap.status.${s}`);
</script>

{#snippet hint(key: string, text: string)}
  <small class="muted">{locked(key) ? t('appearance.locked') : text}</small>
{/snippet}

{#snippet invalid(key: string)}
  {#if errorField === key}<small class="error">{error}</small>{/if}
{/snippet}

{#snippet steps(r: Report)}
  {#each r.servers as srv (srv.url)}
    <h3>{srv.url}</h3>
    <ul class="ldap-steps">
      {#each srv.steps as st (st.name)}
        <li class={st.status}>
          <span class="ldap-status">{statusText(st.status)}</span>
          <span>
            <strong>{t(`ldap.step.${st.name}` as Key)}</strong>
            {#if st.name === 'users' && srv.users !== undefined}· {t('ldap.users_count', { n: String(srv.users) })}{/if}
            {#if st.name === 'tls' && srv.cert}
              <small class="muted">
                {t('ldap.cert', { issuer: srv.cert.issuer, date: srv.cert.not_after, days: String(srv.cert.days_left), version: srv.cert.tls_version })}
              </small>
            {/if}
            {#if codeText(st.code)}<small>{codeText(st.code)}</small>{/if}
            {#if st.detail}<pre class="smtp-detail"><code>{st.detail}</code></pre>{/if}
          </span>
        </li>
      {/each}
    </ul>
  {/each}
{/snippet}

<div class="email-settings directory">
  <h1>{t('ldap.title')}</h1>
  <p class="lead">{t('ldap.intro')}</p>

  {#if form}
    <p class={form.enabled ? 'notice' : 'muted'} role="status">{form.enabled ? t('ldap.on') : t('ldap.off')}</p>
    {#if form.insecure_skip_verify}<p class="error" role="alert">{t('ldap.insecure_warning')}</p>{/if}

    <form
      class="stack"
      onsubmit={(e) => {
        e.preventDefault();
        save(false);
      }}
    >
      <label class="check">
        <input type="checkbox" disabled={locked('enabled')} bind:checked={form.enabled} />
        {t('ldap.enabled')}
      </label>
      {@render hint('enabled', t('ldap.enabled_hint'))}

      <h2>{t('ldap.server')}</h2>
      <fieldset class="field">
        <legend>{t('ldap.flavor')}</legend>
        {#each flavors as f (f)}
          <label class="check">
            <input type="radio" name="flavor" value={f} disabled={locked('flavor')} bind:group={form.flavor} />
            {t(`ldap.flavor.${f}`)}
          </label>
        {/each}
        {@render hint('flavor', t('ldap.flavor_hint'))}
      </fieldset>
      <label class="field">
        {t('ldap.urls')}
        <textarea
          rows="2"
          spellcheck="false"
          placeholder="ldaps://dc01.empresa.local:636"
          disabled={locked('urls')}
          aria-invalid={errorField === 'urls'}
          bind:value={urlsText}
        ></textarea>
        {@render hint('urls', t('ldap.urls_hint'))}
        {@render invalid('urls')}
      </label>
      <label class="check">
        <input type="checkbox" disabled={locked('start_tls')} bind:checked={form.start_tls} />
        {t('ldap.start_tls')}
      </label>
      {@render hint('start_tls', t('ldap.start_tls_hint'))}
      {@render invalid('start_tls')}
      <label class="field">
        {t('ldap.ca_file')}
        <input spellcheck="false" placeholder="/etc/kudawiki/ad-ca.pem" disabled={locked('ca_file')} aria-invalid={errorField === 'ca_file'} bind:value={form.ca_file} />
        {@render hint('ca_file', t('ldap.ca_file_hint'))}
        {@render invalid('ca_file')}
      </label>
      <label class="check">
        <input type="checkbox" disabled={locked('insecure_skip_verify')} bind:checked={form.insecure_skip_verify} />
        {t('ldap.insecure_skip_verify')}
      </label>
      {@render hint('insecure_skip_verify', t('ldap.insecure_hint'))}

      <h2>{t('ldap.account')}</h2>
      <label class="field">
        {t('ldap.bind_dn')}
        <input
          spellcheck="false"
          autocomplete="off"
          placeholder="CN=svc-kudawiki,OU=Servicios,DC=empresa,DC=local"
          disabled={locked('bind_dn')}
          aria-invalid={errorField === 'bind_dn'}
          bind:value={form.bind_dn}
        />
        {@render hint('bind_dn', t('ldap.bind_dn_hint'))}
        {@render invalid('bind_dn')}
      </label>
      <label class="field">
        {t('ldap.bind_password')}
        <!-- new-password: que el navegador no ofrezca la contraseña de la wiki para este campo. -->
        <input
          type="password"
          maxlength="1024"
          autocomplete="new-password"
          disabled={locked('bind_password')}
          aria-invalid={errorField === 'bind_password'}
          bind:value={form.bind_password}
        />
        {@render hint('bind_password', t('ldap.bind_password_hint'))}
        {@render invalid('bind_password')}
      </label>
      {#if form.flavor === 'ad'}
        <label class="field">
          {t('ldap.domain_netbios')}
          <input class="port" maxlength="15" spellcheck="false" placeholder="EMPRESA" disabled={locked('domain_netbios')} bind:value={form.domain_netbios} />
          {@render hint('domain_netbios', t('ldap.domain_netbios_hint'))}
          {@render invalid('domain_netbios')}
        </label>
      {/if}

      <h2>{t('ldap.search')}</h2>
      <label class="field">
        {t('ldap.user_base_dn')}
        <input
          spellcheck="false"
          placeholder="OU=Usuarios,DC=empresa,DC=local"
          disabled={locked('user_base_dn')}
          aria-invalid={errorField === 'user_base_dn'}
          bind:value={form.user_base_dn}
        />
        {@render hint('user_base_dn', t('ldap.user_base_dn_hint'))}
        {@render invalid('user_base_dn')}
      </label>
      <label class="field">
        {t('ldap.user_filter')}
        <input spellcheck="false" placeholder={defaults?.user_filter} disabled={locked('user_filter')} aria-invalid={errorField === 'user_filter'} bind:value={form.user_filter} />
        {@render hint('user_filter', t('ldap.default_hint'))}
        {@render invalid('user_filter')}
      </label>
      <label class="field">
        {t('ldap.login_attrs')}
        <input
          spellcheck="false"
          placeholder={defaults?.login_attrs.join(', ')}
          disabled={locked('login_attrs')}
          aria-invalid={errorField === 'login_attrs'}
          bind:value={loginAttrsText}
        />
        {@render hint('login_attrs', t('ldap.login_attrs_hint'))}
        {@render invalid('login_attrs')}
      </label>
      <label class="field">
        {t('ldap.group_base_dn')}
        <input
          spellcheck="false"
          placeholder="OU=Grupos,DC=empresa,DC=local"
          disabled={locked('group_base_dn')}
          aria-invalid={errorField === 'group_base_dn'}
          bind:value={form.group_base_dn}
        />
        {@render hint('group_base_dn', t('ldap.group_base_dn_hint'))}
        {@render invalid('group_base_dn')}
      </label>
      <label class="field">
        {t('ldap.disabled_filter')}
        <input
          spellcheck="false"
          placeholder={defaults?.disabled_filter}
          disabled={locked('disabled_filter')}
          aria-invalid={errorField === 'disabled_filter'}
          bind:value={form.disabled_filter}
        />
        {@render hint('disabled_filter', t('ldap.disabled_filter_hint'))}
        {@render invalid('disabled_filter')}
      </label>

      <h2>{t('ldap.attrs')}</h2>
      {#each attrKeys as k (k)}
        <label class="field">
          {t(`ldap.attrs_${k}`)}
          <input
            class="attr"
            spellcheck="false"
            placeholder={defaults?.attrs[k] || t('ldap.attrs_guid_dn')}
            disabled={locked(`attrs.${k}`)}
            aria-invalid={errorField === `attrs.${k}`}
            bind:value={form.attrs[k]}
          />
          {@render hint(`attrs.${k}`, k === 'guid' ? t('ldap.attrs_guid_hint') : t('ldap.default_hint'))}
          {@render invalid(`attrs.${k}`)}
        </label>
      {/each}
      <label class="field">
        {t('ldap.timeout')}
        <input class="port" spellcheck="false" disabled={locked('timeout')} aria-invalid={errorField === 'timeout'} bind:value={form.timeout} />
        {@render hint('timeout', t('ldap.timeout_hint'))}
        {@render invalid('timeout')}
      </label>

      {#if error && !errorField}<p class="error" role="alert">{error}</p>{/if}
      {#if error && errorField}<p class="error" role="alert">{t('ldap.fix_field', { field: fieldLabel(errorField) })}</p>{/if}
      {#if notice && !dirty}<p class="notice" role="status">{notice}</p>{/if}
      <div class="conflict-actions">
        <button type="button" class="secondary" disabled={testing || busy} onclick={testConnection}>
          {testing ? t('ldap.testing') : t('ldap.test')}
        </button>
        <button type="submit" class="primary" disabled={busy || !dirty}>{busy ? t('common.saving') : t('common.save')}</button>
        {#if failedSave}
          <button type="button" class="link danger" disabled={busy} onclick={() => (confirmForce = true)}>{t('ldap.force')}</button>
        {/if}
      </div>
    </form>

    {#if report}
      <section aria-labelledby="ldap-report-title" class="ldap-report">
        <h2 id="ldap-report-title">{report.ok ? t('ldap.report_ok') : t('ldap.report_fail')}</h2>
        {#if report.suggestion}
          <p>
            <!-- AD no publica vendorName: sin él, el tipo basta para nombrarlo. -->
            {t(report.suggestion.vendor ? 'ldap.suggestion' : 'ldap.suggestion_type', {
              vendor: report.suggestion.vendor ?? '',
              flavor: t(`ldap.flavor.${report.suggestion.flavor}`),
              guid: report.suggestion.guid || t('ldap.attrs_guid_dn'),
            })}
            {#if report.suggestion.flavor !== form.flavor || (report.suggestion.guid && report.suggestion.guid !== (form.attrs.guid || defaults?.attrs.guid))}
              <button type="button" class="link" onclick={useSuggestion}>{t('ldap.use_suggestion')}</button>
            {/if}
          </p>
        {/if}
        {@render steps(report)}
      </section>
    {/if}

    <section aria-labelledby="ldap-user-title">
      <h2 id="ldap-user-title">{t('ldap.user_test')}</h2>
      <p>{t('ldap.user_test_hint')}</p>
      <form class="ldap-user-form" onsubmit={testUser}>
        <label class="field">
          {t('ldap.login')}
          <input required maxlength="256" spellcheck="false" autocomplete="off" placeholder="ana · EMPRESA\ana · ana@empresa.local" bind:value={login} />
        </label>
        <button type="submit" class="secondary" disabled={lookingUp || !login.trim()}>{lookingUp ? t('ldap.testing') : t('ldap.user_test_run')}</button>
      </form>
      {#if userError}
        <div role="alert">
          <p class="error">{userError}</p>
          {#if userDetail}<pre class="smtp-detail"><code>{userDetail}</code></pre>{/if}
        </div>
      {/if}
      {#if userReport}
        <div class="ldap-user" role="status">
          {#if userReport.status === 'not_found'}
            <p class="error">{t('ldap.user_not_found', { login: userReport.login })}</p>
          {:else if userReport.status === 'ambiguous'}
            <p class="error">{t('ldap.user_ambiguous')}</p>
            <ul>
              {#each userReport.dns ?? [] as dn (dn)}<li><code>{dn}</code></li>{/each}
            </ul>
          {:else}
            <p class={userReport.enabled ? 'notice' : 'error'}>{userReport.enabled ? t('ldap.user_enabled') : t('ldap.user_disabled')}</p>
            <dl class="ldap-facts">
              <dt>DN</dt>
              <dd><code>{userReport.dn}</code></dd>
              <dt>{t('ldap.attrs_email')}</dt>
              <dd>{userReport.email || '—'}</dd>
              <dt>{t('ldap.attrs_name')}</dt>
              <dd>{userReport.name || '—'}</dd>
              <dt>{t('ldap.attrs_guid')}</dt>
              <dd><code>{userReport.guid || t('ldap.attrs_guid_dn')}</code></dd>
              <dt>{t('ldap.user_attributes')}</dt>
              <dd>{userReport.attributes.join(', ')}</dd>
            </dl>
            {#if userReport.missing.length}
              <p class="error">{t('ldap.user_missing', { attrs: userReport.missing.join(', ') })}</p>
            {/if}
            {#if userReport.warnings.includes('guid_from_dn')}<p class="muted">{t('ldap.code.guid_from_dn')}</p>{/if}
          {/if}
          <p class="muted"><small>{t('ldap.user_filter_used')} <code>{userReport.filter}</code> · {userReport.url}</small></p>
        </div>
      {/if}
    </section>
  {:else if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</div>

<Dialog title={t('ldap.force_title')} bind:open={confirmForce}>
  <form
    class="stack"
    onsubmit={(e) => {
      e.preventDefault();
      save(true);
    }}
  >
    <p>{t('ldap.force_body')}</p>
    <button type="submit" class="primary danger-btn" disabled={busy}>{t('ldap.force')}</button>
  </form>
</Dialog>
