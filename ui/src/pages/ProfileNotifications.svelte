<script lang="ts">
  import { api, codeOf, notificationCategories, type EmailPref, type NotificationPrefs } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';

  // Mi perfil → Notificaciones (SPEC §12, §12.1, §15): seguir lo que uno crea o edita, y el correo
  // de cada categoría como tabla. La campana no se apaga. Se guarda con la barra.
  type Editable = Pick<NotificationPrefs, 'auto_watch' | 'email'>;

  let prefs = $state<NotificationPrefs | null>(null);
  let form = $state<Editable | null>(null);
  let saved = $state('');
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');

  const choices: EmailPref[] = ['immediate', 'daily', 'off'];
  // Cada categoría cuenta como un cambio, no el grupo del correo entero.
  const flat = (e: Editable) => ({ auto_watch: e.auto_watch, ...e.email });
  const changes = $derived(form && saved ? countChanges(flat(JSON.parse(saved)), flat($state.snapshot(form))) : 0);

  function show(p: NotificationPrefs) {
    prefs = p;
    form = { auto_watch: p.auto_watch, email: { ...p.email } };
    saved = JSON.stringify(form);
  }

  api<NotificationPrefs>('GET', '/me/notification-prefs').then(show, (e) => (error = errorText(codeOf(e))));

  async function save() {
    if (!form || !prefs) return;
    busy = true;
    notice = error = '';
    try {
      await api('PUT', '/me/notification-prefs', form);
      show({ ...prefs, ...$state.snapshot(form) });
      notice = t('notif.saved');
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }
</script>

<PageHead context={`${t('profile.title')} · ${t('profile.group_account')}`} title={t('notif.prefs_title')} description={t('profile.notif_desc')} />

{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if notice && !changes}<p class="notice" role="status">{notice}</p>{/if}

{#if form && prefs}
  <Setting title={t('profile.follow')} description={t('profile.follow_desc')}>
    <label class="check check-hint">
      <input type="checkbox" bind:checked={form.auto_watch} />
      <span><strong>{t('notif.auto_watch')}</strong><small>{t('profile.follow_hint')}</small></span>
    </label>
  </Setting>

  <Setting title={t('notif.email_title')} description={t('notif.email_note')} wide>
    {#if !prefs.email_enabled}<p class="callout" role="note">{t('notif.email_off')}</p>{/if}
    <div class="table-scroll">
      <table class="admin-table notif-table">
        <thead>
          <tr>
            <th scope="col">{t('profile.notif_col')}</th>
            {#each choices as v (v)}<th scope="col" class="center">{t(`notif.email.${v}`)}</th>{/each}
          </tr>
        </thead>
        <tbody>
          {#each notificationCategories as c (c)}
            <tr>
              <th scope="row">{t(`notif.cat.${c}`)}</th>
              {#each choices as v (v)}
                <td class="center">
                  <input
                    type="radio"
                    name={`notif-${c}`}
                    value={v}
                    disabled={!prefs.email_enabled}
                    aria-label={`${t(`notif.cat.${c}`)}: ${t(`notif.email.${v}`)}`}
                    bind:group={form.email[c]}
                  />
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Setting>
{/if}

<SaveBar count={changes} {busy} onsave={save} ondiscard={() => form && (form = JSON.parse(saved))} />
