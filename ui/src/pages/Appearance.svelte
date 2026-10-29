<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText, has } from '../lib/i18n/index.svelte';
  import { site, logoURL } from '../lib/site.svelte';
  import Logo from '../components/Logo.svelte';

  // Administración → Apariencia (SPEC §15.1): la marca de la empresa, una sola para todas las
  // áreas. Solo valores validados: texto plano, colores #RRGGBB e imágenes que el servidor
  // sanitiza (§15.1.5). Los colores se prueban en la vista previa; al guardar se recarga la
  // página, que el servidor arma ya con la paleta nueva, en claro y en oscuro (§15.1.2).

  type Warning = { code: string; color?: string };
  type Palette = { light: Record<string, string>; warnings: Warning[]; header_dark: boolean };
  type ColorKey = 'color_primary' | 'color_header' | 'color_sidebar';
  type Brand = {
    title: string;
    company: string;
    color_primary: string;
    color_header: string;
    color_sidebar: string;
    login_message: string;
    show_powered_by: boolean;
    locked: string[]; // fijadas en kudawiki.yaml o KUDAWIKI_*: el panel no las cambia (§13.1)
    assets: Record<string, string>;
  };

  // Paletas sugeridas (§15.1.2). La primera es la de Kudawiki: la que pone "Restablecer".
  const palettes = [
    { key: 'blue', primary: '#1d4f91', header: '#15171c' },
    { key: 'green', primary: '#1e6b47', header: '#13261d' },
    { key: 'burgundy', primary: '#8c1d40', header: '#1f1418' },
    { key: 'teal', primary: '#0e5e6f', header: '#102a30' },
    { key: 'light', primary: '#1d4f91', header: '#f7f8fa' },
  ] as const;
  const kinds = ['logo', 'logo_dark', 'favicon'] as const;
  type Kind = (typeof kinds)[number];

  let form = $state<Brand>({
    title: '',
    company: '',
    color_primary: palettes[0].primary,
    color_header: palettes[0].header,
    color_sidebar: '',
    login_message: '',
    show_powered_by: true,
    locked: [],
    assets: {},
  });
  let loaded = $state(false);
  let ownSidebar = $state(false);
  let palette = $state<Palette>();
  let busy = $state(false);
  let error = $state('');
  let assetError = $state('');
  let notes = $state<Partial<Record<Kind, Warning[]>>>({});
  let preview = $state<HTMLElement>();

  const isHex = (v: string) => /^#[0-9a-fA-F]{6}$/.test(v);
  const locked = (key: string) => form.locked.includes(key);

  api<Brand>('GET', '/admin/site')
    .then((s) => {
      form = s;
      ownSidebar = !!s.color_sidebar;
      loaded = true;
    })
    .catch((e) => (error = errorText(codeOf(e))));

  // La paleta la calcula el servidor, con la misma cuenta que usa para la página: espera a que
  // el selector se quede quieto para no pedir una por cada paso.
  $effect(() => {
    const body = { color_primary: form.color_primary, color_header: form.color_header, color_sidebar: ownSidebar ? form.color_sidebar : '' };
    if (!loaded || !isHex(body.color_primary) || !isHex(body.color_header) || (ownSidebar && !isHex(body.color_sidebar))) return;
    const timer = setTimeout(() => {
      api<Palette>('POST', '/admin/site/preview', body)
        .then((p) => (palette = p))
        .catch((e) => (error = errorText(codeOf(e))));
    }, 200);
    return () => clearTimeout(timer);
  });

  // Por CSSOM y no con un atributo style: la CSP no deja estilos en línea (SPEC §14.5).
  $effect(() => {
    if (preview && palette) for (const [k, v] of Object.entries(palette.light)) preview.style.setProperty(k, v);
  });
  function swatch(node: HTMLElement, color: string) {
    node.style.setProperty('background', color);
  }

  function warningText(w: Warning): string {
    const key = `appearance.warning.${w.code}`;
    return has(key) ? t(key, { color: w.color ?? '' }) : w.code;
  }

  function usePalette(p: (typeof palettes)[number]) {
    if (!locked('color_primary')) form.color_primary = p.primary;
    if (!locked('color_header')) form.color_header = p.header;
  }

  // Deja el formulario con la marca de Kudawiki; se aplica al guardar. Un título vacío es el de
  // Kudawiki. Los logos se quitan aparte, uno por uno.
  function reset() {
    const kudawiki = {
      title: '',
      company: '',
      color_primary: palettes[0].primary,
      color_header: palettes[0].header,
      color_sidebar: '',
      login_message: '',
      show_powered_by: true,
    };
    Object.assign(form, Object.fromEntries(Object.entries(kudawiki).filter(([k]) => !locked(k))));
    if (!locked('color_sidebar')) ownSidebar = false;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    const all = {
      title: form.title,
      company: form.company,
      color_primary: form.color_primary,
      color_header: form.color_header,
      color_sidebar: ownSidebar ? form.color_sidebar : '',
      login_message: form.login_message,
      show_powered_by: form.show_powered_by,
    };
    busy = true;
    error = '';
    try {
      // Una clave fijada en el archivo no se manda: el servidor la rechazaría aunque no cambie.
      await api('PATCH', '/admin/site', Object.fromEntries(Object.entries(all).filter(([k]) => !locked(k))));
      location.reload();
    } catch (err) {
      error = errorText(codeOf(err));
      busy = false;
    }
  }

  function showAssets(assets: Record<string, string>) {
    form.assets = assets;
    site.logo = assets.logo ?? '';
    site.logo_dark = assets.logo_dark ?? '';
  }

  async function upload(kind: Kind, input: HTMLInputElement) {
    const file = input.files?.[0];
    if (!file) return;
    const body = new FormData();
    body.append('file', file);
    busy = true;
    assetError = '';
    notes[kind] = [];
    try {
      const res = await api<{ warnings: Warning[]; site: Brand }>('PUT', `/admin/site/assets/${kind}`, body);
      showAssets(res.site.assets);
      notes[kind] = res.warnings;
    } catch (err) {
      const code = codeOf(err);
      // El texto general de archivos habla de PDF y Office: aquí solo van imágenes.
      assetError = code === 'unsupported_file_type' ? t('appearance.unsupported') : errorText(code);
    } finally {
      input.value = '';
      busy = false;
    }
  }

  async function remove(kind: Kind) {
    busy = true;
    assetError = '';
    notes[kind] = [];
    try {
      await api('DELETE', `/admin/site/assets/${kind}`);
      showAssets((await api<Brand>('GET', '/admin/site')).assets);
    } catch (err) {
      assetError = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  function assetSrc(kind: Kind): string {
    if (kind === 'favicon') return form.assets.icons ? `/_site/icon-32.png?v=${form.assets.icons}` : '';
    const v = form.assets[kind];
    return v ? `/_site/${kind === 'logo' ? 'logo' : 'logo-dark'}?v=${v}` : '';
  }
</script>

{#snippet colorField(key: ColorKey, label: string, hint: string)}
  <div class="field">
    <span id={`${key}-label`}>{label}</span>
    <span class="color-field">
      <input
        type="color"
        aria-labelledby={`${key}-label`}
        disabled={locked(key)}
        value={isHex(form[key]) ? form[key].toLowerCase() : '#000000'}
        oninput={(e) => (form[key] = e.currentTarget.value)}
      />
      <input
        type="text"
        aria-label={t('appearance.hex', { name: label })}
        aria-invalid={!isHex(form[key])}
        maxlength="7"
        spellcheck="false"
        disabled={locked(key)}
        bind:value={form[key]}
      />
    </span>
    <small class="muted">{locked(key) ? t('appearance.locked') : hint}</small>
  </div>
{/snippet}

<div class="appearance">
  <h1>{t('appearance.title')}</h1>
  <p class="lead">{t('appearance.intro')}</p>

  <form class="stack" onsubmit={save}>
    <h2>{t('appearance.identity')}</h2>
    <label class="field">
      {t('appearance.site_title')}
      <input maxlength="40" placeholder="Kudawiki" disabled={locked('title')} bind:value={form.title} />
      <small class="muted">{locked('title') ? t('appearance.locked') : t('appearance.site_title_hint')}</small>
    </label>
    <label class="field">
      {t('appearance.company')}
      <input maxlength="80" disabled={locked('company')} bind:value={form.company} />
      <small class="muted">{locked('company') ? t('appearance.locked') : t('appearance.company_hint')}</small>
    </label>
    <label class="field">
      {t('appearance.login_message')}
      <textarea rows="2" maxlength="500" disabled={locked('login_message')} bind:value={form.login_message}></textarea>
      <small class="muted">{locked('login_message') ? t('appearance.locked') : t('appearance.login_message_hint')}</small>
    </label>
    <label class="check">
      <input type="checkbox" disabled={locked('show_powered_by')} bind:checked={form.show_powered_by} />
      {t('appearance.powered_by')}
    </label>

    <h2>{t('appearance.colors')}</h2>
    <fieldset>
      <legend class="side-title">{t('appearance.suggested')}</legend>
      <ul class="palettes">
        {#each palettes as p (p.key)}
          <li>
            <button type="button" class="secondary" disabled={locked('color_primary') && locked('color_header')} onclick={() => usePalette(p)}>
              <span class="swatch" use:swatch={p.header}></span><span class="swatch" use:swatch={p.primary}></span>
              {t(`appearance.palette.${p.key}`)}
            </button>
          </li>
        {/each}
      </ul>
    </fieldset>
    {@render colorField('color_primary', t('appearance.primary'), t('appearance.primary_hint'))}
    {@render colorField('color_header', t('appearance.header'), t('appearance.header_hint'))}
    <label class="check">
      <input type="checkbox" disabled={locked('color_sidebar')} bind:checked={ownSidebar} onchange={() => (form.color_sidebar ||= form.color_primary)} />
      {t('appearance.own_sidebar')}
    </label>
    {#if ownSidebar}{@render colorField('color_sidebar', t('appearance.sidebar'), t('appearance.sidebar_hint'))}{/if}
    {#each palette?.warnings ?? [] as w (w.code)}<p class="notice" role="status">{warningText(w)}</p>{/each}
    <p class="muted small">{t('appearance.fixed_colors')}</p>

    <h2>{t('appearance.preview')}</h2>
    <!-- Miniatura con las variables de la paleta; lo que dice para un lector de pantalla son los avisos de arriba. -->
    <div class="brand-preview" bind:this={preview} aria-hidden="true">
      <div class="brand-preview-bar">
        {#if logoURL(palette?.header_dark ?? site.header_dark)}
          <img class="brand-logo" src={logoURL(palette?.header_dark ?? site.header_dark)} alt="" />
        {:else}
          <Logo size={22} />
        {/if}
        {form.title || 'Kudawiki'}
        <span class="brand-preview-muted">{t('nav.area')}</span>
      </div>
      <div class="brand-preview-body">
        <div class="brand-preview-side">
          <span class="current">{t('appearance.preview_page')}</span>
          <span>{t('appearance.preview_other')} <span class="level">🔒 N2</span></span>
        </div>
        <div class="brand-preview-main">
          <strong>{t('appearance.preview_page')}</strong>
          <p>{t('appearance.preview_text')} <span class="brand-preview-link">{t('appearance.preview_link')}</span>.</p>
          <span class="brand-preview-button">{t('common.save')}</span>
        </div>
      </div>
    </div>

    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="conflict-actions">
      <button type="submit" class="primary" disabled={busy || !loaded}>{busy ? t('common.saving') : t('common.save')}</button>
      <button type="button" class="secondary" disabled={busy || !loaded} onclick={reset}>{t('appearance.reset')}</button>
    </div>
    <small class="muted">{t('appearance.reset_hint')}</small>
  </form>

  <section aria-labelledby="logos-title">
    <h2 id="logos-title">{t('appearance.logos')}</h2>
    {#if assetError}<p class="error" role="alert">{assetError}</p>{/if}
    {#each kinds as kind (kind)}
      <div class="asset">
        <div class={['asset-view', kind === 'logo_dark' && 'dark']}>
          {#if assetSrc(kind)}<img src={assetSrc(kind)} alt="" />{:else}<span class="small">{t('appearance.kudawiki_asset')}</span>{/if}
        </div>
        <div class="field">
          <label for={`file-${kind}`}>{t(`appearance.kind.${kind}`)}</label>
          <input
            id={`file-${kind}`}
            type="file"
            accept="image/svg+xml,image/png,image/webp"
            aria-describedby={`hint-${kind}`}
            disabled={busy || !loaded}
            onchange={(e) => upload(kind, e.currentTarget)}
          />
          <small id={`hint-${kind}`} class="muted">{t(`appearance.kind_hint.${kind}`)}</small>
          {#each notes[kind] ?? [] as w (w.code)}<p class="notice" role="status">{warningText(w)}</p>{/each}
          {#if form.assets[kind]}
            <button
              type="button"
              class="link"
              aria-label={t('appearance.remove_label', { kind: t(`appearance.kind.${kind}`) })}
              disabled={busy}
              onclick={() => remove(kind)}>{t('appearance.remove')}</button
            >
          {/if}
        </div>
      </div>
    {/each}
  </section>
</div>
