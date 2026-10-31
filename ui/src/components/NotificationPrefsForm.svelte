<script lang="ts">
  import { api, codeOf, notificationCategories, type EmailPref, type NotificationCategory, type NotificationPrefs } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Preferencias de avisos del perfil (SPEC §12, §12.1): seguir lo que uno crea o edita, y el
  // correo de cada categoría. Se guardan al cambiar, como las de lectura. La campana no se apaga.
  let prefs = $state<NotificationPrefs | null>(null);
  let status = $state('');
  let error = $state('');

  $effect(() => {
    api<NotificationPrefs>('GET', '/me/notification-prefs').then(
      (p) => (prefs = p),
      (e) => (error = errorText(codeOf(e))),
    );
  });

  const choices: EmailPref[] = ['immediate', 'daily', 'off'];

  async function save() {
    if (!prefs) return;
    status = error = '';
    try {
      await api('PUT', '/me/notification-prefs', { auto_watch: prefs.auto_watch, email: prefs.email });
      status = t('notif.saved');
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  function setEmail(c: NotificationCategory, v: EmailPref) {
    if (!prefs) return;
    prefs.email[c] = v;
    save();
  }
</script>

<section class="prefs-panel notif-prefs" id="notifications" aria-labelledby="notif-prefs-title">
  <h2 id="notif-prefs-title">{t('notif.prefs_title')}</h2>
  {#if prefs}
    <label class="check">
      <input type="checkbox" bind:checked={prefs.auto_watch} onchange={save} />
      {t('notif.auto_watch')}
    </label>
    <p class="muted small">{t('notif.inbox_note')}</p>
    <h3 class="side-title">{t('notif.email_title')}</h3>
    {#if !prefs.email_enabled}<p class="muted small">{t('notif.email_off')}</p>{/if}
    {#each notificationCategories as c (c)}
      <fieldset>
        <legend>{t(`notif.cat.${c}`)}</legend>
        <div class="lang-options">
          {#each choices as v (v)}
            <label class="prefs-option">
              <input type="radio" name={`notif-${c}`} checked={prefs.email[c] === v} onchange={() => setEmail(c, v)} />
              {t(`notif.email.${v}`)}
            </label>
          {/each}
        </div>
      </fieldset>
    {/each}
    <p class="muted small">{t('notif.email_note')}</p>
  {/if}
  {#if status}<p role="status" class="muted small">{status}</p>{/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>
