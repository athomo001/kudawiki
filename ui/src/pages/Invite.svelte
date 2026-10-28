<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import Logo from '../components/Logo.svelte';

  // El token viaja en el fragmento (#), que nunca llega al servidor; se saca de la barra de
  // direcciones apenas se lee, para que no quede en el historial (SPEC §8.5).
  const token = location.hash.slice(1);
  history.replaceState(null, '', '/invite');

  let password = $state('');
  let repeat = $state('');
  let busy = $state(false);
  let done = $state(false);
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
      await api('POST', '/auth/invite/accept', { token, new_password: password });
      done = true;
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<main class="login">
  <p class="login-brand"><Logo size={40} /> {t('app.name')}</p>
  <h1>{t('invite.title')}</h1>
  {#if done}
    <p role="status">{t('invite.done')}</p>
    <p><a href="/">{t('invite.go_login')}</a></p>
  {:else}
    <form onsubmit={submit}>
      <label for="pw">{t('auth.password')}</label>
      <input id="pw" type="password" autocomplete="new-password" required bind:value={password} />
      <label for="pw2">{t('auth.password_repeat')}</label>
      <input id="pw2" type="password" autocomplete="new-password" required bind:value={repeat} />
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button type="submit" class="primary" disabled={busy || !token}>{t('invite.submit')}</button>
    </form>
  {/if}
</main>
