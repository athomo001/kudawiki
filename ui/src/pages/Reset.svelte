<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { site } from '../lib/site.svelte';
  import Brand from '../components/Brand.svelte';

  // Contraseña nueva con el enlace del correo (SPEC §8.5.1). El token viaja en el fragmento (#),
  // que nunca llega al servidor; se saca de la barra de direcciones apenas se lee, para que no
  // quede en el historial, y se manda por POST.
  const token = location.hash.slice(1);
  history.replaceState(null, '', '/reset');

  let password = $state('');
  let repeat = $state('');
  let busy = $state(false);
  let done = $state(false);
  let expired = $state(!token);
  let error = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (password !== repeat) {
      error = t('invite.mismatch');
      return;
    }
    busy = true;
    error = '';
    try {
      await api('POST', '/auth/password-reset/complete', { token, new_password: password });
      done = true;
    } catch (err) {
      const code = codeOf(err);
      if (code === 'invalid_reset') expired = true;
      else error = errorText(code);
    } finally {
      busy = false;
    }
  }
</script>

<main class="login">
  <p class="login-brand"><Brand size={40} /></p>
  <h1>{t('reset.title')}</h1>
  {#if done}
    <p role="status">{t('reset.done')}</p>
    <p><a href="/">{t('invite.go_login')}</a></p>
  {:else if expired}
    <p role="alert">{t('reset.expired')}</p>
    {#if site.password_reset}<p><a href="/forgot">{t('reset.ask_again')}</a></p>{/if}
  {:else}
    <form onsubmit={submit}>
      <label for="pw">{t('reset.new_password')}</label>
      <input id="pw" type="password" autocomplete="new-password" required bind:value={password} />
      <label for="pw2">{t('auth.password_repeat')}</label>
      <input id="pw2" type="password" autocomplete="new-password" required bind:value={repeat} />
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button type="submit" class="primary" disabled={busy}>{t('reset.submit')}</button>
    </form>
    <p class="muted small">{t('reset.sessions_note')}</p>
  {/if}
</main>
