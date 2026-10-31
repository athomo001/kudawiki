<script lang="ts">
  import { api, ApiError, codeOf, mediaMarkdown, timeAgo, type Area, type MediaFile, type PageLink } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import Icon from './Icon.svelte';

  // Navegador de archivos del área (SPEC §4.4): lo que quien mira puede ver, con miniaturas (nunca
  // el original) y la página que tiene cada archivo. Con onpick, elige un archivo para el editor;
  // sin él, se copia el enlace o se borra.
  let { area, onpick }: { area: Area; onpick?: (f: MediaFile) => void } = $props();

  let files = $state<MediaFile[]>([]);
  let q = $state('');
  let loaded = $state(false);
  let error = $state('');
  let asking = $state(''); // id del archivo cuyo borrado se está confirmando
  let blocked = $state<{ id: string; pages: PageLink[] } | null>(null);
  let copied = $state('');
  const base = $derived(`/w/${area.slug}`);
  // El servidor manda los 200 más nuevos; con más, se busca por nombre.
  const LIMIT = 200;

  let timer: ReturnType<typeof setTimeout>;
  async function load() {
    try {
      files = await api<MediaFile[]>('GET', `${base}/media?q=${encodeURIComponent(q.trim())}`);
      loaded = true;
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  $effect(() => {
    void q;
    clearTimeout(timer);
    timer = setTimeout(load, 250);
    return () => clearTimeout(timer);
  });

  const bytes = (n: number) => {
    const [value, unit] = n < 1024 ? [n, 'byte'] : n < 1024 ** 2 ? [n / 1024, 'kilobyte'] : [n / 1024 ** 2, 'megabyte'];
    return new Intl.NumberFormat(locale(), { style: 'unit', unit, unitDisplay: 'short', maximumFractionDigits: 1 }).format(value);
  };

  async function copy(f: MediaFile) {
    try {
      await navigator.clipboard.writeText(mediaMarkdown(f));
      copied = f.id;
    } catch {
      error = t('files.copy_failed');
    }
  }

  // Si una página que quien borra ve lo usa, el servidor dice cuáles: primero se saca de ahí.
  async function remove(f: MediaFile) {
    error = '';
    blocked = null;
    try {
      await api('DELETE', `${base}/media/${f.id}`);
      asking = '';
      files = files.filter((x) => x.id !== f.id);
    } catch (e) {
      asking = '';
      if (e instanceof ApiError && e.code === 'media_in_use') blocked = { id: f.id, pages: (e.detail.pages as PageLink[]) ?? [] };
      else error = errorText(codeOf(e));
    }
  }
</script>

<div class="files">
  <label class="files-search">
    <Icon name="search" size={16} />
    <span class="sr-only">{t('files.search')}</span>
    <input type="search" placeholder={t('files.search')} bind:value={q} />
  </label>
  {#if error}<p class="error" role="alert">{error}</p>{/if}

  {#if loaded && !files.length}
    <p class="muted">{q.trim() ? t('files.none_found') : t('files.none')}</p>
  {/if}
  <ul class="file-list">
    {#each files as f (f.id)}
      <li class="file">
        <div class="file-thumb">
          {#if f.thumb}<img src={`${f.url}?thumb=1`} alt="" loading="lazy" />{:else}<Icon name="page" size={28} />{/if}
        </div>
        <div class="file-info">
          <span class="file-name">{f.name}</span>
          <span class="muted small">{bytes(f.size)} · {f.uploaded_by} · {timeAgo(f.created_at, locale())}</span>
          <span class="small">
            {#if f.page}{t('files.in_page')} <a href={`${base}/p/${f.page.slug}`}>{f.page.title}</a>
            {:else}<span class="muted">{t('files.unused')}</span>{/if}
          </span>
          {#if blocked?.id === f.id}
            <p class="error small" role="alert">
              {t('files.in_use')}
              {#each blocked.pages as p, i (p.slug)}{i ? ', ' : ''}<a href={`${base}/edit/${p.slug}`}>{p.title}</a>{/each}
            </p>
          {/if}
        </div>
        <div class="file-actions">
          {#if onpick}
            <button type="button" class="secondary" onclick={() => onpick(f)}>{t('files.insert')}</button>
          {:else if asking === f.id}
            <span class="small">{t('files.confirm', { name: f.name })}</span>
            <button type="button" class="danger-btn" onclick={() => remove(f)}>{t('files.delete_yes')}</button>
            <button type="button" class="link" onclick={() => (asking = '')}>{t('common.cancel')}</button>
          {:else}
            <button type="button" class="link" onclick={() => copy(f)}>{copied === f.id ? t('files.copied') : t('files.copy')}</button>
            <button type="button" class="link" onclick={() => ((asking = f.id), (blocked = null))}>{t('files.delete')}</button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
  {#if files.length >= LIMIT}<p class="muted small">{t('files.more', { n: String(LIMIT) })}</p>{/if}
</div>
