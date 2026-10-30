<script lang="ts">
  import { api, codeOf, ApiError } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Administración → Correo (SPEC §12): el servidor SMTP con que la wiki manda sus correos y el
  // correo de prueba. La contraseña nunca llega al navegador: viene enmascarada y, si no se toca,
  // se devuelve la máscara, que el servidor lee como "sin cambios" (§1).

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

  function show(s: Settings) {
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

  async function save(e: SubmitEvent) {
    e.preventDefault();
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

<div class="email-settings">
  <h1>{t('smtp.title')}</h1>
  <p class="lead">{t('smtp.intro')}</p>

  {#if loaded}
    <p class={form.enabled ? 'notice' : 'muted'} role="status">{form.enabled ? t('smtp.enabled') : t('smtp.disabled')}</p>
  {/if}

  <form class="stack" onsubmit={save}>
    <h2>{t('smtp.server')}</h2>
    <label class="field">
      {t('smtp.host')}
      <input maxlength="253" spellcheck="false" autocomplete="off" placeholder="smtp.empresa.cl" disabled={locked('host')} bind:value={form.host} />
      {@render hint('host', t('smtp.host_hint'))}
    </label>
    <fieldset class="field">
      <legend>{t('smtp.security')}</legend>
      {#each securities as s (s)}
        <label class="check">
          <input
            type="radio"
            name="security"
            value={s}
            checked={form.security === s}
            disabled={locked('security')}
            onchange={() => changeSecurity(s)}
          />
          {t(`smtp.security.${s}`)}
        </label>
      {/each}
      {@render hint('security', t(`smtp.security_hint.${form.security}`))}
    </fieldset>
    <label class="field">
      {t('smtp.port')}
      <input type="number" min="1" max="65535" class="port" disabled={locked('port')} bind:value={form.port} />
      {@render hint('port', t('smtp.port_hint'))}
    </label>

    <h2>{t('smtp.account')}</h2>
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

    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if notice && !dirty}<p class="notice" role="status">{notice}</p>{/if}
    <div class="conflict-actions">
      <button type="submit" class="primary" disabled={busy || !loaded || !dirty}>{busy ? t('common.saving') : t('common.save')}</button>
    </div>
  </form>

  <section aria-labelledby="email-test-title">
    <h2 id="email-test-title">{t('smtp.test')}</h2>
    <p>{t('smtp.test_hint', { email })}</p>
    <button type="button" class="secondary" disabled={!form.enabled || dirty || testState === 'sending'} onclick={sendTest}>
      {testState === 'sending' ? t('smtp.test_sending') : t('smtp.test_send')}
    </button>
    {#if dirty}<small class="muted">{t('smtp.test_save_first')}</small>{/if}
    {#if testState === 'sent'}<p class="notice" role="status">{t('smtp.test_sent', { email })}</p>{/if}
    {#if testState === 'failed'}
      <div role="alert">
        <p class="error">{testError}</p>
        {#if testDetail}<pre class="smtp-detail"><code>{testDetail}</code></pre>{/if}
      </div>
    {/if}
  </section>
</div>
