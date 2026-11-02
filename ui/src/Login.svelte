<script lang="ts">
  import { api, codeOf } from './lib/api';
  import { t, errorText } from './lib/i18n/index.svelte';
  import Brand from './components/Brand.svelte';
  import { site } from './lib/site.svelte';
  import { passkeysSupported, signWithPasskey } from './lib/passkey';

  let { onlogin }: { onlogin: () => void } = $props();

  let email = $state('');
  let password = $state('');
  // Segundo paso (SPEC §8.1): la contraseña ya pasó y falta el código de la app (o uno de
  // recuperación) o una passkey, según lo que la persona tenga.
  let step = $state<'password' | 'code'>('password');
  let methods = $state<string[]>([]);
  const canPasskey = passkeysSupported();
  let code = $state('');
  let busy = $state(false);
  let error = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      const res = await api<{ mfa_required?: boolean; methods?: string[] } | undefined>('POST', '/auth/login', { email, password });
      password = '';
      if (res?.mfa_required) {
        methods = res.methods ?? [];
        step = 'code';
        return;
      }
      onlogin();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  async function submitCode(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await api('POST', '/auth/totp', { code });
      onlogin();
    } catch (err) {
      // El paso venció (5 minutos) o el servidor se reinició: se vuelve a la contraseña.
      if (codeOf(err) === 'mfa_expired') back();
      error = errorText(codeOf(err));
    } finally {
      code = '';
      busy = false;
    }
  }

  // Con passkey: sin contraseña desde el primer paso, o como segundo paso tras ella. El servidor
  // distingue los dos casos por la cookie del segundo paso.
  async function withPasskey() {
    busy = true;
    error = '';
    try {
      await signWithPasskey('/auth/passkey/begin', '/auth/passkey/finish');
      onlogin();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  function back() {
    step = 'password';
    code = '';
    error = '';
  }
</script>

<main class="login">
  <p class="login-brand"><Brand size={40} /></p>
  {#if step === 'code'}
    <h1>{t('totp.login_title')}</h1>
    {#if methods.includes('totp')}
      <form onsubmit={submitCode} aria-describedby={error ? 'login-error' : undefined}>
        <label for="code">{t('totp.code')}</label>
        <!-- Sin inputmode numérico: el código de recuperación lleva letras. -->
        <!-- svelte-ignore a11y_autofocus -->
        <input id="code" autocomplete="one-time-code" required autofocus aria-describedby="code-hint" bind:value={code} />
        <small id="code-hint" class="muted">{t('totp.login_hint')}</small>

        {#if error}<p id="login-error" class="error" role="alert">{error}</p>{/if}

        <button type="submit" class="primary" disabled={busy}>{busy ? t('auth.logging_in') : t('totp.verify')}</button>
      </form>
    {:else}
      <p>{t('passkeys.login_step')}</p>
      {#if error}<p id="login-error" class="error" role="alert">{error}</p>{/if}
    {/if}
    {#if methods.includes('passkey') && canPasskey}
      <button type="button" class={methods.includes('totp') ? 'login-passkey' : 'primary login-passkey'} disabled={busy} onclick={withPasskey}
        >{t('passkeys.use')}</button
      >
    {:else if !methods.includes('totp')}
      <p class="error" role="alert">{t('passkeys.unsupported')}</p>
    {/if}
    <p class="login-alt"><button type="button" class="link" onclick={back}>{t('totp.back')}</button></p>
  {:else}
    <h1>{t('auth.login')}</h1>
    <form onsubmit={submit} aria-describedby={error ? 'login-error' : undefined}>
      <!-- Con el directorio activo se entra también con el usuario de la red (ana, EMPRESAna). -->
      <label for="email">{site.directory ? t('auth.email_or_user') : t('auth.email')}</label>
      <input id="email" type={site.directory ? 'text' : 'email'} autocomplete="username" autocapitalize="none" spellcheck="false" required bind:value={email} />

      <label for="password">{t('auth.password')}</label>
      <input id="password" type="password" autocomplete="current-password" required bind:value={password} />

      {#if error}<p id="login-error" class="error" role="alert">{error}</p>{/if}

      <button type="submit" class="primary" disabled={busy}>
        {busy ? t('auth.logging_in') : t('auth.login')}
      </button>
    </form>
    {#if site.passkeys && canPasskey}
      <button type="button" class="login-passkey" disabled={busy} onclick={withPasskey}>{t('passkeys.login')}</button>
    {/if}
    {#if site.password_reset}<p class="login-alt"><a href="/forgot">{t('reset.forgot_link')}</a></p>{/if}
    <!-- La contraseña del directorio no se recupera en la wiki: se manda a la página de la empresa (§8.6.3). -->
    {#if site.password_help_url}
      <p class="login-alt"><a href={site.password_help_url} rel="noopener noreferrer" target="_blank">{t('auth.password_help')}</a></p>
    {/if}
  {/if}
  <!-- Aviso de uso de la empresa (§15.1.1): texto plano, nunca HTML. -->
  {#if site.login_message}<p class="login-message">{site.login_message}</p>{/if}
</main>
