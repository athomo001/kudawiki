<script lang="ts">
  import { api, codeOf, type Me } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';
  import Avatar from '../components/Avatar.svelte';
  import Icon from '../components/Icon.svelte';

  // Mi perfil → Perfil (SPEC §8.5.2, §15): foto, nombre y áreas. El correo y los roles los cambia
  // quien administra; aquí solo se muestran. La foto se aplica al momento; el nombre, con la barra.
  let { me, avatarRev, onchanged }: { me: Me; avatarRev: number; onchanged: () => Promise<void> } = $props();

  // Igual que auth.MaxAvatarBytes. Se revisa antes de subir: el servidor deja de leer al pasarse
  // y el navegador podría ver una conexión cortada en vez del aviso.
  const MAX_PHOTO = 5 << 20;

  // Parte con el nombre guardado; al recargar el perfil vuelve a él.
  let name = $derived(me.full_name);
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');
  let photoBusy = $state(false);
  let photoError = $state('');
  let file: HTMLInputElement;

  const changes = $derived(me.auth_source === 'local' && name !== me.full_name ? 1 : 0);

  async function save() {
    busy = true;
    notice = error = '';
    try {
      await api('PATCH', '/me', { full_name: name });
      await onchanged();
      notice = t('profile.saved');
    } catch (err) {
      const code = codeOf(err);
      // El texto general de invalid_user habla también del correo, que aquí no se toca.
      error = code === 'invalid_user' ? t('profile.invalid_name') : errorText(code);
    } finally {
      busy = false;
    }
  }

  async function upload(input: HTMLInputElement) {
    const f = input.files?.[0];
    input.value = '';
    if (!f) return;
    photoError = '';
    if (f.size > MAX_PHOTO) {
      photoError = errorText('file_too_large');
      return;
    }
    const body = new FormData();
    body.append('file', f);
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
</script>

<PageHead context={`${t('profile.title')} · ${t('profile.group_account')}`} title={t('profile.nav_account')} description={t('profile.account_desc')} />

{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if notice && !changes}<p class="notice" role="status">{notice}</p>{/if}

<Setting title={t('profile.photo')} description={t('profile.photo_desc')}>
  <div class="profile-photo">
    <Avatar {me} rev={avatarRev} large />
    <div class="stack">
      <div class="profile-photo-actions">
        <button type="button" class="secondary" disabled={photoBusy} onclick={() => file.click()}>
          <Icon name="image" size={16} />
          {me.has_avatar ? t('profile.photo_change') : t('profile.photo_upload')}
        </button>
        {#if me.has_avatar}
          <button type="button" class="link danger" disabled={photoBusy} onclick={() => photo(() => api('DELETE', '/me/avatar'))}>{t('profile.photo_remove')}</button>
        {/if}
      </div>
      <small class="muted">{t('profile.photo_hint')}</small>
      <!-- El selector del navegador queda oculto: lo abre el botón. -->
      <input
        bind:this={file}
        class="sr-only"
        type="file"
        tabindex="-1"
        aria-hidden="true"
        accept="image/png,image/jpeg,image/webp"
        onchange={(e) => upload(e.currentTarget)}
      />
      {#if photoError}<p class="error" role="alert">{photoError}</p>{/if}
    </div>
  </div>
</Setting>

<Setting title={t('profile.data')} description={me.auth_source === 'ldap' ? t('profile.directory_hint') : t('profile.data_desc')}>
  {#if me.auth_source === 'ldap'}
    <!-- El nombre y el correo se traen del directorio en cada ingreso (§8.6.3): aquí solo se ven. -->
    <div class="field"><span>{t('common.name')}</span><span class="profile-value">{me.full_name}</span></div>
  {:else}
    <label class="field">
      {t('common.name')}
      <input required maxlength="120" autocomplete="name" bind:value={name} />
    </label>
  {/if}
  <div class="field">
    <span>{t('auth.email')}</span>
    <span class="profile-value">{me.email}</span>
    <small class="muted locked-hint"><Icon name="lock" size={14} /> {t('profile.email_hint')}</small>
  </div>
</Setting>

<Setting title={t('profile.areas')} description={t('profile.areas_hint')}>
  <ul class="plain profile-areas">
    {#each me.workspaces as w (w.slug)}
      <li>
        <a href={`/w/${w.slug}`}><strong>{w.name}</strong></a>
        <span class="pill plain neutral">{t(`role.${w.role}`)}</span>
      </li>
    {:else}
      <li class="muted">{t('home.no_areas')}</li>
    {/each}
  </ul>
</Setting>

<SaveBar count={changes} {busy} onsave={save} ondiscard={() => (name = me.full_name)} />
