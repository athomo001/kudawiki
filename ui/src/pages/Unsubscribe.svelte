<script lang="ts">
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { notificationCategories, type NotificationCategory } from '../lib/api';
  import Brand from '../components/Brand.svelte';

  // Baja de los correos de avisos (SPEC §12.1, RFC 8058). Abrir el enlace solo muestra esta
  // confirmación; el botón hace el mismo POST que manda el cliente de correo con un clic. No
  // pide sesión: lo autoriza la firma del enlace. El enlace sale de la barra de direcciones apenas
  // se lee, para que no quede en el historial.
  const token = new URLSearchParams(location.search).get('t') ?? '';
  history.replaceState(null, '', '/unsubscribe');
  const category = token.split('.')[1] ?? '';
  const known = category === 'all' || (notificationCategories as readonly string[]).includes(category);

  let busy = $state(false);
  let done = $state(false);
  let invalid = $state(!known);
  let error = $state('');

  async function confirm() {
    busy = true;
    error = '';
    try {
      const res = await fetch(`/unsubscribe?t=${encodeURIComponent(token)}`, { method: 'POST' });
      if (res.status === 404) invalid = true;
      else if (!res.ok) error = errorText('internal');
      else done = true;
    } catch {
      error = errorText('network');
    } finally {
      busy = false;
    }
  }
</script>

<main class="login">
  <p class="login-brand"><Brand size={40} /></p>
  <h1>{t('unsub.title')}</h1>
  {#if done}
    <p role="status">{t('unsub.done')}</p>
    <p><a href="/">{t('unsub.home')}</a></p>
  {:else if invalid}
    <p role="alert">{t('unsub.invalid')}</p>
    <p><a href="/">{t('unsub.home')}</a></p>
  {:else}
    <p>{t('unsub.body', { what: category === 'all' ? t('unsub.all') : t(`notif.cat.${category as NotificationCategory}`) })}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="button" class="primary" disabled={busy} onclick={confirm}>{t('unsub.confirm')}</button>
  {/if}
</main>
