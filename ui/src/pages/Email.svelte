<script lang="ts">
  import { api, codeOf, ApiError } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';

  // Administración → Correo (SPEC §12): el servidor SMTP con que la wiki manda sus correos y el
  // correo de prueba. La contraseña nunca llega al navegador: viene enmascarada y, si no se toca,
  // se devuelve la máscara, que el servidor lee como "sin cambios" (§1). Diseño "Kudawiki —
  // Administración" (§15): secciones en dos columnas y barra de guardado.

  type Security = 'starttls' | 'tls' | 'none';
  type Settings = {
    host: string;
    port: number;
    security: Security;
    username: string;
    password: string;
    from: string;
    locked: string[]; // fijadas en kudawiki.yaml o KUDAWIKI_SMTP_*: el panel no las cambia (§13.1)
    enabled: boolean;
  };
  const securities: Security[] = ['starttls', 'tls', 'none'];
  // El puerto de cada modo: al cambiar de modo se cambia el puerto si seguía en el de antes.
  const defaultPort: Record<Security, number> = { starttls: 587, tls: 465, none: 25 };
  const keys = ['host', 'port', 'security', 'username', 'password', 'from'] as const;

  let { email }: { email: string } = $props();

  let form = $state<Settings>({ host: '', port: 587, security: 'starttls', username: '', password: '', from: '', locked: [], enabled: false });
  let saved = $state('');
  let loaded = $state(false);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let testState = $state<'idle' | 'sending' | 'sent' | 'failed'>('idle');
  let testError = $state('');
  let testDetail = $state('');

  const locked = (key: string) => form.locked.includes(key);
  const editable = () => Object.fromEntries(keys.filter((k) => !locked(k)).map((k) => [k, form[k]]));
  const dirty = $derived(loaded && JSON.stringify(editable()) !== saved);
  const changes = $derived(loaded ? countChanges(JSON.parse(saved), editable()) : 0);
  // original: lo último que vino del servidor, para Descartar.
  let original = '';

  function show(s: Settings) {
    original = JSON.stringify(s);
    form = s;
    saved = JSON.stringify(editable());
    loaded = true;
  }

  api<Settings>('GET', '/admin/smtp')
    .then(show)
    .catch((e) => (error = errorText(codeOf(e))));

  function changeSecurity(next: Security) {
    if (!locked('port') && form.port === defaultPort[form.security]) form.port = defaultPort[next];
    form.security = next;
  }

  async function save() {
    busy = true;
    error = '';
    notice = '';
    try {
      // Una clave fijada en el archivo no se manda: el servidor la rechazaría aunque no cambie.
      show(await api<Settings>('PUT', '/admin/smtp', editable()));
      notice = t('smtp.saved');
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  async function sendTest() {
    testState = 'sending';
    testError = '';
    testDetail = '';
    try {
      await api('POST', '/admin/smtp/test');
      testState = 'sent';
    } catch (err) {
      testState = 'failed';
      testError = errorText(codeOf(err));
      // Lo que contestó el servidor SMTP: es lo que hace falta para corregir la configuración.
      if (err instanceof ApiError && typeof err.detail.detail === 'string') testDetail = err.detail.detail;
    }
  }
</script>

{#snippet hint(key: string, text: string)}
  <small class="muted">{locked(key) ? t('appearance.locked') : text}</small>
{/snippet}

<PageHead context={`${t('nav.admin')} · ${t('admin.group_site')}`} title={t('smtp.title')} description={t('smtp.intro')}>
  {#snippet status()}
    {#if loaded}<span class={`pill ${form.enabled ? 'ok' : 'neutral'}`}>{form.enabled ? t('smtp.state_on') : t('smtp.state_off')}</span>{/if}
  {/snippet}
  {#snippet actions()}
    <button type="button" class="primary" disabled={busy || !loaded || !dirty} onclick={() => save()}>{busy ? t('common.saving') : t('common.save')}</button>
  {/snippet}
</PageHead>

{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if notice && !dirty}<p class="notice" role="status">{notice}</p>{/if}

<form
  class="email-settings"
  onsubmit={(e) => {
    e.preventDefault();
    save();
  }}
>
  <Setting title={t('smtp.server')} description={t('smtp.server_desc')}>
    <label class="field">
      {t('smtp.host')}
      <input maxlength="253" spellcheck="false" autocomplete="off" placeholder="smtp.empresa.cl" disabled={locked('host')} bind:value={form.host} />
      {@render hint('host', t('smtp.host_hint'))}
    </label>
    <fieldset class="field">
      <legend>{t('smtp.security')}</legend>
      <div class="choices">
        {#each securities as s (s)}
          <label class="choice">
            <input
              type="radio"
              name="security"
              value={s}
              checked={form.security === s}
              disabled={locked('security')}
              onchange={() => changeSecurity(s)}
            />
            <span><strong>{t(`smtp.security.${s}`)}</strong><small>{t(`smtp.security_hint.${s}`)}</small></span>
          </label>
        {/each}
      </div>
      {#if locked('security')}<small class="muted">{t('appearance.locked')}</small>{/if}
    </fieldset>
    <label class="field">
      {t('smtp.port')}
      <input type="number" min="1" max="65535" class="port" disabled={locked('port')} bind:value={form.port} />
      {@render hint('port', t('smtp.port_hint'))}
    </label>
  </Setting>

  <Setting title={t('smtp.account')} description={t('smtp.account_desc')}>
    <label class="field">
      {t('smtp.username')}
      <input maxlength="254" spellcheck="false" autocomplete="off" disabled={locked('username')} bind:value={form.username} />
      {@render hint('username', t('smtp.username_hint'))}
    </label>
    <label class="field">
      {t('smtp.password')}
      <!-- new-password: que el navegador no ofrezca la contraseña de la wiki para este campo. -->
      <input type="password" maxlength="1024" autocomplete="new-password" disabled={locked('password')} bind:value={form.password} />
      {@render hint('password', t('smtp.password_hint'))}
    </label>
    <label class="field">
      {t('smtp.from')}
      <input type="email" maxlength="254" spellcheck="false" placeholder="wiki@empresa.cl" disabled={locked('from')} bind:value={form.from} />
      {@render hint('from', t('smtp.from_hint'))}
    </label>
  </Setting>
</form>

<Setting title={t('smtp.test')} description={t('smtp.test_hint', { email })}>
  <div class="inline">
    <button type="button" class="secondary" disabled={!form.enabled || dirty || testState === 'sending'} onclick={sendTest}>
      {testState === 'sending' ? t('smtp.test_sending') : t('smtp.test_send')}
    </button>
    {#if dirty}<small class="muted">{t('smtp.test_save_first')}</small>{/if}
  </div>
  {#if testState === 'sent'}<p class="notice" role="status">{t('smtp.test_sent', { email })}</p>{/if}
  {#if testState === 'failed'}
    <div role="alert">
      <p class="error">{testError}</p>
      {#if testDetail}<pre class="smtp-detail"><code>{testDetail}</code></pre>{/if}
    </div>
  {/if}
</Setting>

<SaveBar count={changes} {busy} onsave={() => save()} ondiscard={() => show(JSON.parse(original))} />
