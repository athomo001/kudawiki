<script lang="ts">
  import { api, codeOf } from './lib/api';
  import { t, errorText } from './lib/i18n/index.svelte';
  import Logo from './components/Logo.svelte';

  let { onlogin }: { onlogin: () => void } = $props();

  let email = $state('');
  let password = $state('');
  let busy = $state(false);
  let error = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await api('POST', '/auth/login', { email, password });
      password = '';
      onlogin();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<main class="login">
  <p class="login-brand"><Logo size={40} /> {t('app.name')}</p>
  <h1>{t('auth.login')}</h1>
  <form onsubmit={submit} aria-describedby={error ? 'login-error' : undefined}>
    <label for="email">{t('auth.email')}</label>
    <input id="email" type="email" autocomplete="username" required bind:value={email} />

    <label for="password">{t('auth.password')}</label>
    <input id="password" type="password" autocomplete="current-password" required bind:value={password} />

    {#if error}<p id="login-error" class="error" role="alert">{error}</p>{/if}

    <button type="submit" class="primary" disabled={busy}>
      {busy ? t('auth.logging_in') : t('auth.login')}
    </button>
  </form>
</main>
