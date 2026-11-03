<script lang="ts">
  import PageHead from '../components/PageHead.svelte';
  import { api, codeOf, type Area } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Exportar e importar el área como Markdown (SPEC §13.8). Importar siempre pasa primero por la
  // simulación: se ve qué páginas se crearían y qué se salta, y recién ahí se confirma con el
  // mismo archivo.
  // adminAreas: con más de una, "Exportar todas mis áreas" baja un ZIP con el de cada una (F8-B).
  let { area, adminAreas, context }: { area: Area; adminAreas: number; context: string } = $props();

  type Report = {
    pages: string[];
    skipped: { path: string; reason: string }[];
    media: number;
    warnings: { path: string; reason: string; detail?: string }[];
  };

  let file = $state<File | null>(null);
  let report = $state<Report | null>(null);
  let done = $state(false);
  let busy = $state(false);
  let error = $state('');

  async function run(dryRun: boolean) {
    if (!file) return;
    busy = true;
    error = '';
    try {
      const body = new FormData();
      body.append('file', file);
      report = await api<Report>('POST', `/w/${area.slug}/import${dryRun ? '?dry_run=1' : ''}`, body);
      done = !dryRun;
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  // Los motivos son códigos estables del servidor; "duplicate_path:<archivo>" trae el otro archivo.
  const reasons = ['invalid_path', 'duplicate_path', 'too_large', 'bad_front_matter', 'exists', 'tags', 'description', 'level', 'owner', 'media'] as const;
  type Reason = (typeof reasons)[number];
  function reason(r: string) {
    const [code = '', other] = r.split(':');
    const text = reasons.includes(code as Reason) ? t(`import.reason.${code as Reason}`) : errorText(code);
    return text + (other ? ` (${other})` : '');
  }
</script>

<PageHead {context} title={t('import.title')} description={t('import.intro')} />

<h2>{t('import.export')}</h2>
<p>{t('import.export_detail')}</p>
<p class="inline">
  <a class="secondary" href={`/api/w/${area.slug}/export`} download>{t('import.export_action')}</a>
  {#if adminAreas > 1}<a class="secondary" href="/api/me/areas/export" download>{t('import.export_all', { n: String(adminAreas) })}</a>{/if}
</p>

<h2>{t('import.import')}</h2>
<p>{t('import.import_detail')}</p>
<form
  onsubmit={(e) => {
    e.preventDefault();
    run(true);
  }}
>
  <label class="field"
    >{t('import.file')}
    <input
      type="file"
      accept=".zip,application/zip"
      required
      onchange={(e) => {
        file = e.currentTarget.files?.[0] ?? null;
        report = null;
        done = false;
      }}
    />
  </label>
  <p class="form-actions"><button type="submit" class="secondary" disabled={busy || !file}>{t('import.simulate')}</button></p>
</form>

{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if report}
  <h3>{done ? t('import.done') : t('import.would')}</h3>
  <p role="status">{t('import.summary', { pages: String(report.pages.length), media: String(report.media), skipped: String(report.skipped.length) })}</p>
  {#if report.pages.length}
    <details>
      <summary>{t('import.pages')}</summary>
      <ul class="mono">{#each report.pages as p (p)}<li>{p}</li>{/each}</ul>
    </details>
  {/if}
  {#if report.skipped.length}
    <h4>{t('import.skipped')}</h4>
    <ul>{#each report.skipped as s (s.path)}<li><span class="mono">{s.path}</span> — {reason(s.reason)}</li>{/each}</ul>
  {/if}
  {#if report.warnings.length}
    <h4>{t('import.warnings')}</h4>
    <ul>
      {#each report.warnings as w, i (i)}<li><span class="mono">{w.path}</span> — {reason(w.reason)}{w.detail ? ` (${w.detail})` : ''}</li>{/each}
    </ul>
  {/if}
  {#if !done && report.pages.length}
    <p class="form-actions">
      <button type="button" disabled={busy} onclick={() => run(false)}>{t('import.confirm', { n: String(report.pages.length) })}</button>
    </p>
  {/if}
{/if}
