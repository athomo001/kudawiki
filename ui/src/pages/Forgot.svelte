<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import Brand from '../components/Brand.svelte';

  // "¿Olvidaste tu contraseña?" (SPEC §8.5.1). La respuesta es siempre la misma, exista o no la
  // cuenta: esta pantalla no puede servir para averiguar quién tiene una.

  let email = $state('');
  let busy = $state(false);
  let sent = $state(false);
  let error = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await api('POST', '/auth/password-reset', { email });
      sent = true;
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<main class="login">
  <p class="login-brand"><Brand size={40} /></p>
  <h1>{t('reset.forgot_title')}</h1>
  {#if sent}
    <p role="status">{t('reset.sent')}</p>
  {:else}
    <p class="muted">{t('reset.forgot_intro')}</p>
    <form onsubmit={submit}>
      <label for="email">{t('auth.email')}</label>
      <input id="email" type="email" autocomplete="username" required bind:value={email} />
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button type="submit" class="primary" disabled={busy}>{t('reset.send_link')}</button>
    </form>
  {/if}
  <p class="login-alt"><a href="/">{t('reset.back_login')}</a></p>
</main>
