<script lang="ts">
  import { untrack } from 'svelte';
  import PageHead from '../components/PageHead.svelte';
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, has, errorText, locale } from '../lib/i18n/index.svelte';

  // Salud del contenido (SPEC §13.7). La del admin de área trae su área; la del superadmin,
  // todas y lo que no es de ningún área. Qué entra lo decide el servidor.
  let { endpoint, context, global = false }: { endpoint: string; context: string; global?: boolean } = $props();

  type Problem = { check: string; severity: 'error' | 'warning'; area?: string; page?: string; detail?: string };
  // El estado de los respaldos, solo en la vista del superadmin (§21.2.4).
  type JobRun = { at: string; file?: string; source?: 'local' | 'remote' | 'cli'; ok: boolean; error?: string };
  type Backups = { daily?: JobRun; upload?: JobRun; verify?: JobRun };
  let backups = $state<Backups | null>(null);
  let problems = $state<Problem[] | null>(null);
  let error = $state('');

  const days = (at: string) => Math.max(0, Math.floor((Date.now() - new Date(at).getTime()) / 86_400_000));
  const errors = $derived(problems?.filter((p) => p.severity === 'error').length ?? 0);
  const warnings = $derived((problems?.length ?? 0) - errors);
  // Primero lo que está roto para quien lee.
  const sorted = $derived([...(problems ?? [])].sort((a, b) => (a.severity === b.severity ? 0 : a.severity === 'error' ? -1 : 1)));

  // Un chequeo que la interfaz no conoce se muestra con su código: nunca se esconde un problema.
  const label = (check: string) => {
    const key = `health.${check}`;
    return has(key) ? t(key) : check;
  };

  $effect(() => {
    void endpoint;
    untrack(() => {
      problems = null;
      api<{ problems: Problem[]; backups?: Backups }>('GET', endpoint)
        .then((r) => ((problems = r.problems), (backups = r.backups ?? null)))
        .catch((e) => (error = errorText(codeOf(e))));
    });
  });
</script>

<PageHead {context} title={t('health.title')} description={t('health.intro')} />
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if problems}
  <p class="health-summary" role="status">
    {#if problems.length}
      <strong class:sev-error={errors > 0}>{t('health.errors', { n: String(errors) })}</strong> · <span>{t('health.warnings', { n: String(warnings) })}</span>
    {:else}
      {t('health.none')}
    {/if}
  </p>
  {#if backups}
    <!-- La última restauración probada es la evidencia de §21.2.4: un respaldo que nunca se probó no cuenta. -->
    <dl class="facts">
      <dt>{t('health.backup_last')}</dt>
      <dd>{#if backups.daily?.ok}{formatDate(backups.daily.at, locale())}{:else}<span class="muted">{t('health.backup_never')}</span>{/if}</dd>
      {#if backups.upload}
        <dt>{t('health.backup_upload')}</dt>
        <dd>{#if backups.upload.ok}{formatDate(backups.upload.at, locale())}{:else}<span class="sev-error">{t('health.backup_failed')}</span>{/if}</dd>
      {/if}
      <dt>{t('health.backup_verified')}</dt>
      <dd>
        {#if backups.verify?.ok}{t('health.backup_days_ago', { n: String(days(backups.verify.at)) })}
          <small class="muted">({t(`health.backup_source_${backups.verify.source ?? 'local'}`)})</small>
        {:else if backups.verify}<span class="sev-error">{t('health.backup_failed')}</span>
        {:else}<span class="muted">{t('health.backup_never')}</span>{/if}
      </dd>
    </dl>
  {/if}
  {#if problems.length}
    <table>
      <thead>
        <tr>
          <th scope="col">{t('health.severity')}</th>
          <th scope="col">{t('health.problem')}</th>
          {#if global}<th scope="col">{t('audit.area')}</th>{/if}
          <th scope="col">{t('health.where')}</th>
        </tr>
      </thead>
      <tbody>
        {#each sorted as p, i (i)}
          <tr>
            <td><span class={`sev sev-${p.severity}`}>{t(`health.sev_${p.severity}`)}</span></td>
            <td>{label(p.check)}{#if p.detail && !p.page}<br /><small class="muted">{p.detail}</small>{/if}</td>
            {#if global}<td>{p.area ?? '—'}</td>{/if}
            <td>
              {#if p.check.startsWith('proposals.') && p.area}
                <!-- Detail es el id: se corrige en la propuesta, y su página puede no existir todavía. -->
                <a href={`/w/${p.area}/proposals/${p.detail}`}>{p.page}</a>
              {:else if p.page && p.area}
                <a href={`/w/${p.area}/p/${p.page}`}>{p.page}</a>
                {#if p.detail}<small class="muted"> → {p.detail}</small>{/if}
              {:else}<span class="muted">—</span>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
  <p class="muted small">{t('health.cli_hint')} <code>kudawiki doctor</code> · <code>kudawiki doctor --fix</code></p>
{/if}
