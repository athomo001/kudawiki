<script lang="ts">
  import { api, codeOf, ApiError } from '../lib/api';
  import { t, has, errorText, type Key } from '../lib/i18n/index.svelte';
  import Dialog from '../components/Dialog.svelte';
  import LdapRules from '../components/LdapRules.svelte';
  import LdapSync from '../components/LdapSync.svelte';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';
  import { countChanges } from '../lib/changes';

  // Administración → Directorio (SPEC §8.6.4, §8.6.5): la conexión a LDAP / Active Directory y
  // su diagnóstico. Las pruebas usan lo que está en el formulario, sin guardarlo. La contraseña
  // de la cuenta de servicio nunca llega al navegador: viene enmascarada y la máscara de vuelta
  // es "sin cambios". Diseño "Kudawiki — Administración" (§15): cuatro pestañas, cada una en su
  // dirección (/admin/ldap, /rules, /sync, /test), con el mismo formulario detrás.

  let { tab }: { tab: 'connection' | 'rules' | 'sync' | 'test' } = $props();

  type Flavor = 'ad' | 'openldap' | 'generic';
  type Attrs = { email: string; name: string; guid: string; photo: string };
  type Defaults = { user_filter: string; login_attrs: string[]; attrs: Attrs; disabled_filter: string; group_member_filter: string };
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
    group_member_filter: string;
    timeout: string;
    password_help_url: string;
    precreate_users: boolean;
    sync_interval: string;
    sync_max_removal_ratio: number;
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
    groups: { guid: string; dn: string; name: string }[];
    access: { workspace_id: string; workspace_name: string; role: 'viewer' | 'editor' | 'admin'; via: string[] }[];
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
    'group_member_filter',
    'timeout',
    'password_help_url',
    'precreate_users',
  ] as const;
  const attrKeys = ['email', 'name', 'guid', 'photo'] as const;

  let form = $state<Settings | null>(null);
  // Las listas se editan como texto: una URL por línea, los atributos separados por comas.
  let urlsText = $state('');
  let loginAttrsText = $state('');
  // El tope de bajas se edita en porcentaje; se guarda como fracción (§8.3.4).
  let ratioPct = $state(10);
  let saved = $state('');
  // loaded: lo último que vino del servidor, para Descartar.
  let loaded = '';
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
    if (!locked('sync.interval')) out.sync_interval = form.sync_interval;
    if (!locked('sync.max_removal_ratio')) out.sync_max_removal_ratio = ratioPct / 100;
    return out;
  }
  const dirty = $derived(form !== null && JSON.stringify(editable()) !== saved);
  const changes = $derived(form && saved ? countChanges(JSON.parse(saved), editable()) : 0);

  function show(s: Settings) {
    loaded = JSON.stringify(s);
    form = s;
    urlsText = s.urls.join('\n');
    loginAttrsText = s.login_attrs.join(', ');
    ratioPct = Math.round(s.sync_max_removal_ratio * 100);
    saved = JSON.stringify(editable());
  }

  api<Settings>('GET', '/admin/ldap/config')
    .then(show)
    .catch((e) => (error = errorText(codeOf(e))));

  function discard() {
    show(JSON.parse(loaded));
    error = '';
    errorField = '';
  }

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
    const key = `ldap.${field.replaceAll('.', '_')}`;
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

<PageHead context={`${t('nav.admin')} · ${t('admin.group_access')}`} title={t('ldap.title')} description={t('ldap.intro')}>
  {#snippet status()}
    {#if form}<span class={`pill ${form.enabled ? 'ok' : 'neutral'}`}>{form.enabled ? t('ldap.state_on') : t('ldap.state_off')}</span>{/if}
  {/snippet}
  {#snippet actions()}
    {#if form && tab === 'connection'}
      <button type="button" class="secondary" disabled={testing || busy} onclick={testConnection}>{testing ? t('ldap.testing') : t('ldap.test')}</button>
      <button type="button" class="primary" disabled={busy || !dirty} onclick={() => save(false)}>{busy ? t('common.saving') : t('common.save')}</button>
    {/if}
  {/snippet}
  {#snippet tabs()}
    <nav class="page-tabs" aria-label={t('ldap.tabs')}>
      <a href="/admin/ldap" aria-current={tab === 'connection' ? 'page' : undefined}>{t('ldap.tab_connection')}</a>
      <a href="/admin/ldap/rules" aria-current={tab === 'rules' ? 'page' : undefined}>{t('ldap.rules')}</a>
      <a href="/admin/ldap/sync" aria-current={tab === 'sync' ? 'page' : undefined}>{t('ldap.sync_settings')}</a>
      <a href="/admin/ldap/test" aria-current={tab === 'test' ? 'page' : undefined}>{t('ldap.user_test')}</a>
    </nav>
  {/snippet}
</PageHead>

<div class="directory">
  {#if form}
    {#if form.insecure_skip_verify}<p class="error" role="alert">{t('ldap.insecure_warning')}</p>{/if}
    {#if error && !errorField}<p class="error" role="alert">{error}</p>{/if}
    {#if error && errorField}<p class="error" role="alert">{t('ldap.fix_field', { field: fieldLabel(errorField) })}</p>{/if}
    {#if failedSave}
      <p class="error">
        <button type="button" class="link danger" disabled={busy} onclick={() => (confirmForce = true)}>{t('ldap.force')}</button>
      </p>
    {/if}
    {#if notice && !dirty}<p class="notice" role="status">{notice}</p>{/if}

    <!-- Un solo formulario para Conexión y Sincronización: lo que se cambió en una pestaña sigue
         ahí al pasar a la otra, y se guarda junto. -->
    <form
      onsubmit={(e) => {
        e.preventDefault();
        save(false);
      }}
    >
      {#if tab === 'connection'}
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

        <Setting title={t('ldap.tab_connection')} description={t('ldap.enabled_hint')}>
          <label class="check">
            <input type="checkbox" disabled={locked('enabled')} bind:checked={form.enabled} />
            {t('ldap.enabled')}
          </label>
          {#if locked('enabled')}<small class="muted">{t('appearance.locked')}</small>{/if}
        </Setting>

        <Setting title={t('ldap.server')} description={t('ldap.server_desc')}>
          <fieldset class="field">
            <legend>{t('ldap.flavor')}</legend>
            <div class="choices">
              {#each flavors as f (f)}
                <label class="choice">
                  <input type="radio" name="flavor" value={f} disabled={locked('flavor')} bind:group={form.flavor} />
                  <span><strong>{t(`ldap.flavor.${f}`)}</strong></span>
                </label>
              {/each}
            </div>
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
          <label class="check check-hint">
            <input type="checkbox" disabled={locked('start_tls')} bind:checked={form.start_tls} />
            <span><strong>{t('ldap.start_tls')}</strong>{@render hint('start_tls', t('ldap.start_tls_hint'))}</span>
          </label>
          {@render invalid('start_tls')}
          <label class="field">
            {t('ldap.ca_file')}
            <input spellcheck="false" placeholder="/etc/kudawiki/ad-ca.pem" disabled={locked('ca_file')} aria-invalid={errorField === 'ca_file'} bind:value={form.ca_file} />
            {@render hint('ca_file', t('ldap.ca_file_hint'))}
            {@render invalid('ca_file')}
          </label>
          <label class="check check-hint warn">
            <input type="checkbox" disabled={locked('insecure_skip_verify')} bind:checked={form.insecure_skip_verify} />
            <span><strong>{t('ldap.insecure_skip_verify')}</strong>{@render hint('insecure_skip_verify', t('ldap.insecure_hint'))}</span>
          </label>
        </Setting>

        <Setting title={t('ldap.account')} description={t('ldap.account_desc')}>
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
        </Setting>

        <Setting title={t('ldap.search')} description={t('ldap.search_desc')}>
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
          {#if form.flavor !== 'ad'}
            <!-- AD saca los grupos de tokenGroups: el filtro no se usa ahí (§8.6.1). -->
            <label class="field">
              {t('ldap.group_member_filter')}
              <input
                spellcheck="false"
                placeholder={defaults?.group_member_filter}
                disabled={locked('group_member_filter')}
                aria-invalid={errorField === 'group_member_filter'}
                bind:value={form.group_member_filter}
              />
              {@render hint('group_member_filter', t('ldap.group_member_filter_hint', { dn: '{dn}', uid: '{uid}' }))}
              {@render invalid('group_member_filter')}
            </label>
          {/if}
        </Setting>

        <Setting title={t('ldap.attrs')} description={t('ldap.attrs_desc')}>
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
              {@render hint(`attrs.${k}`, k === 'guid' ? t('ldap.attrs_guid_hint') : k === 'photo' ? t('ldap.attrs_photo_hint') : t('ldap.default_hint'))}
              {@render invalid(`attrs.${k}`)}
            </label>
          {/each}
        </Setting>

        <Setting title={t('ldap.other')} description={t('ldap.other_desc')}>
          <label class="field">
            {t('ldap.timeout')}
            <input class="port" spellcheck="false" disabled={locked('timeout')} aria-invalid={errorField === 'timeout'} bind:value={form.timeout} />
            {@render hint('timeout', t('ldap.timeout_hint'))}
            {@render invalid('timeout')}
          </label>
          <label class="field">
            {t('ldap.password_help_url')}
            <input
              type="url"
              spellcheck="false"
              placeholder="https://cuenta.empresa.cl"
              disabled={locked('password_help_url')}
              aria-invalid={errorField === 'password_help_url'}
              bind:value={form.password_help_url}
            />
            {@render hint('password_help_url', t('ldap.password_help_url_hint'))}
            {@render invalid('password_help_url')}
          </label>
        </Setting>
      {:else if tab === 'sync'}
        <Setting title={t('ldap.sync_settings')} description={t('ldap.sync_desc')}>
          <label class="check check-hint">
            <input type="checkbox" disabled={locked('precreate_users')} bind:checked={form.precreate_users} />
            <span><strong>{t('ldap.precreate_users')}</strong>{@render hint('precreate_users', t('ldap.precreate_users_hint'))}</span>
          </label>
          <label class="field">
            {t('ldap.sync_interval')}
            <input class="port" spellcheck="false" disabled={locked('sync.interval')} aria-invalid={errorField === 'sync.interval'} bind:value={form.sync_interval} />
            {@render hint('sync.interval', t('ldap.sync_interval_hint'))}
            {@render invalid('sync.interval')}
          </label>
          <label class="field">
            {t('ldap.sync_max_removal_ratio')}
            <input
              class="port"
              type="number"
              min="0"
              max="100"
              step="1"
              disabled={locked('sync.max_removal_ratio')}
              aria-invalid={errorField === 'sync.max_removal_ratio'}
              bind:value={ratioPct}
            />
            {@render hint('sync.max_removal_ratio', t('ldap.sync_max_removal_ratio_hint'))}
            {@render invalid('sync.max_removal_ratio')}
          </label>
        </Setting>
      {/if}
    </form>

    {#if tab === 'rules'}
      <LdapRules />
    {:else if tab === 'sync'}
      <LdapSync />
      {#if !form.enabled}<p class="muted">{t('ldap.off')}</p>{/if}
    {:else if tab === 'test'}
      <section aria-labelledby="ldap-user-title" class="ldap-user-test">
        <h2 id="ldap-user-title" class="sr-only">{t('ldap.user_test')}</h2>
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
                {#if !userReport.warnings.includes('no_group_base')}
                  <dt>{t('ldap.user_groups')}</dt>
                  <dd>{userReport.groups.map((g) => g.name).join(', ') || '—'}</dd>
                  <dt>{t('ldap.user_access')}</dt>
                  <dd>
                    {#if userReport.access.length}
                      <ul class="ldap-access">
                        {#each userReport.access as a (a.workspace_id)}
                          <li>{t('ldap.user_access_item', { area: a.workspace_name, role: t(`role.${a.role}`), via: a.via.join(', ') })}</li>
                        {/each}
                      </ul>
                    {:else}—{/if}
                  </dd>
                {/if}
              </dl>
              {#if userReport.missing.length}
                <p class="error">{t('ldap.user_missing', { attrs: userReport.missing.join(', ') })}</p>
              {/if}
              {#if userReport.warnings.includes('guid_from_dn')}<p class="muted">{t('ldap.code.guid_from_dn')}</p>{/if}
              {#if userReport.warnings.includes('no_mapped_group')}<p class="error">{t('ldap.code.no_mapped_group')}</p>{/if}
              {#if userReport.warnings.includes('no_group_base')}<p class="muted">{t('ldap.code.no_group_base')}</p>{/if}
            {/if}
            <p class="muted"><small>{t('ldap.user_filter_used')} <code>{userReport.filter}</code> · {userReport.url}</small></p>
          </div>
        {/if}
      </section>
    {/if}

    <SaveBar count={changes} {busy} note={form.enabled ? t('ldap.save_note') : ''} onsave={() => save(false)} ondiscard={discard} />
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
