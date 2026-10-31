<script lang="ts">
  import { api, codeOf, type Me, type ReadingPrefs } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import Avatar from '../components/Avatar.svelte';
  import ReadingPrefsForm from '../components/ReadingPrefsForm.svelte';
  import NotificationPrefsForm from '../components/NotificationPrefsForm.svelte';

  // Mi perfil (SPEC §8.5.2, §14.1): lo que la persona cambia de sí misma. El correo y los roles
  // los cambia quien administra; aquí solo se muestran.
  let {
    me,
    avatarRev,
    onprefs,
    onchanged,
  }: {
    me: Me;
    avatarRev: number;
    onprefs: (change: { locale?: 'es' | 'en'; reading_prefs?: ReadingPrefs }) => void;
    onchanged: () => Promise<void>;
  } = $props();

  // Igual que auth.MaxAvatarBytes. Se revisa antes de subir: el servidor deja de leer al pasarse
  // y el navegador podría ver una conexión cortada en vez del aviso.
  const MAX_PHOTO = 5 << 20;

  // Parte con el nombre guardado; al recargar el perfil vuelve a él.
  let name = $derived(me.full_name);
  let nameBusy = $state(false);
  let nameSaved = $state(false);
  let nameError = $state('');

  let photoBusy = $state(false);
  let photoError = $state('');

  let current = $state('');
  let next = $state('');
  let repeat = $state('');
  let revokeOthers = $state(true);
  let passwordBusy = $state(false);
  let passwordDone = $state(false);
  let passwordError = $state('');

  async function saveName(e: SubmitEvent) {
    e.preventDefault();
    nameBusy = true;
    nameSaved = false;
    nameError = '';
    try {
      await api('PATCH', '/me', { full_name: name });
      await onchanged();
      nameSaved = true;
    } catch (err) {
      const code = codeOf(err);
      // El texto general de invalid_user habla también del correo, que aquí no se toca.
      nameError = code === 'invalid_user' ? t('profile.invalid_name') : errorText(code);
    } finally {
      nameBusy = false;
    }
  }

  async function upload(input: HTMLInputElement) {
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    photoError = '';
    if (file.size > MAX_PHOTO) {
      photoError = errorText('file_too_large');
      return;
    }
    const body = new FormData();
    body.append('file', file);
    await photo(() => api('PUT', '/me/avatar', body));
  }

  async function photo(call: () => Promise<void>) {
    photoBusy = true;
    photoError = '';
    try {
      await call();
      await onchanged();
    } catch (err) {
      const code = codeOf(err);
      // El texto general de archivos habla de PDF y Office: aquí solo van fotos.
      photoError = code === 'unsupported_file_type' ? t('profile.photo_unsupported') : errorText(code);
    } finally {
      photoBusy = false;
    }
  }

  async function changePassword(e: SubmitEvent) {
    e.preventDefault();
    passwordDone = false;
    passwordError = '';
    if (next !== repeat) {
      passwordError = t('invite.mismatch');
      return;
    }
    passwordBusy = true;
    try {
      await api('POST', '/me/password', { current_password: current, new_password: next, revoke_others: revokeOthers });
      current = next = repeat = '';
      passwordDone = true;
    } catch (err) {
      passwordError = errorText(codeOf(err));
    } finally {
      passwordBusy = false;
    }
  }
</script>

<div class="profile">
  <h1>{t('profile.title')}</h1>

  <section class="profile-photo" aria-labelledby="photo-title">
    <Avatar {me} rev={avatarRev} large />
    <div class="field">
      <h2 id="photo-title">{t('profile.photo')}</h2>
      <label for="photo-file">{me.has_avatar ? t('profile.photo_change') : t('profile.photo_upload')}</label>
      <input
        id="photo-file"
        type="file"
        accept="image/png,image/jpeg,image/webp"
        aria-describedby="photo-hint"
        disabled={photoBusy}
        onchange={(e) => upload(e.currentTarget)}
      />
      <small id="photo-hint" class="muted">{t('profile.photo_hint')}</small>
      {#if me.has_avatar}
        <button type="button" class="link" disabled={photoBusy} onclick={() => photo(() => api('DELETE', '/me/avatar'))}>{t('profile.photo_remove')}</button>
      {/if}
      {#if photoError}<p class="error" role="alert">{photoError}</p>{/if}
    </div>
  </section>

  <form class="stack" onsubmit={saveName}>
    <h2>{t('profile.data')}</h2>
    <label class="field">
      {t('common.name')}
      <input required maxlength="120" autocomplete="name" bind:value={name} />
    </label>
    <div class="field">
      <span>{t('auth.email')}</span>
      <span class="profile-value">{me.email}</span>
      <small class="muted">{t('profile.email_hint')}</small>
    </div>
    {#if nameError}<p class="error" role="alert">{nameError}</p>{/if}
    {#if nameSaved}<p role="status" class="muted">{t('profile.saved')}</p>{/if}
    <button type="submit" class="primary" disabled={nameBusy || name.trim() === me.full_name}>{nameBusy ? t('common.saving') : t('common.save')}</button>
  </form>

  <section aria-labelledby="areas-title">
    <h2 id="areas-title">{t('profile.areas')}</h2>
    <ul class="plain">
      {#each me.workspaces as w (w.slug)}
        <li>
          <a href={`/w/${w.slug}`}><strong>{w.name}</strong></a>
          <span class="muted">· {t(`role.${w.role}`)}</span>
        </li>
      {:else}
        <li class="muted">{t('home.no_areas')}</li>
      {/each}
    </ul>
    <p class="muted small">{t('profile.areas_hint')}</p>
  </section>

  <section class="prefs-panel" aria-labelledby="prefs-title">
    <h2 id="prefs-title">{t('profile.reading')}</h2>
    <fieldset>
      <legend class="side-title">{t('lang.ui')}</legend>
      <div class="lang-options">
        {#each ['es', 'en'] as const as l (l)}
          <label class="prefs-option">
            <input type="radio" name="profile-locale" checked={locale() === l} onchange={() => onprefs({ locale: l })} />
            {t(`lang.${l}`)}
          </label>
        {/each}
      </div>
    </fieldset>
    <ReadingPrefsForm prefs={me.reading_prefs} onchange={(p) => onprefs({ reading_prefs: p })} />
  </section>

  <NotificationPrefsForm />

  <form class="stack" onsubmit={changePassword}>
    <h2>{t('profile.password')}</h2>
    <!-- El correo oculto ayuda al gestor de contraseñas a saber de qué cuenta es la clave nueva. -->
    <input class="sr-only" type="email" autocomplete="username" value={me.email} readonly tabindex="-1" aria-hidden="true" />
    <label class="field">
      {t('profile.password_current')}
      <input type="password" autocomplete="current-password" required bind:value={current} />
    </label>
    <label class="field">
      {t('profile.password_new')}
      <input type="password" autocomplete="new-password" required aria-describedby="password-hint" bind:value={next} />
    </label>
    <small id="password-hint" class="muted">{t('profile.password_hint')}</small>
    <label class="field">
      {t('auth.password_repeat')}
      <input type="password" autocomplete="new-password" required bind:value={repeat} />
    </label>
    <label class="check">
      <input type="checkbox" bind:checked={revokeOthers} />
      {t('profile.revoke_others')}
    </label>
    {#if passwordError}<p class="error" role="alert">{passwordError}</p>{/if}
    {#if passwordDone}<p role="status" class="muted">{t('profile.password_done')}</p>{/if}
    <button type="submit" class="primary" disabled={passwordBusy}>{passwordBusy ? t('common.saving') : t('profile.password_submit')}</button>
  </form>
</div>
