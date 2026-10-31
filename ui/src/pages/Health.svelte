<script lang="ts">
  import { untrack } from 'svelte';
  import { api, codeOf } from '../lib/api';
  import { t, has, errorText } from '../lib/i18n/index.svelte';

  // Salud del contenido (SPEC §13.7). La del admin de área trae su área; la del superadmin,
  // todas y lo que no es de ningún área. Qué entra lo decide el servidor.
  let { endpoint, global = false }: { endpoint: string; global?: boolean } = $props();

  type Problem = { check: string; severity: 'error' | 'warning'; area?: string; page?: string; detail?: string };
  let problems = $state<Problem[] | null>(null);
  let error = $state('');

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
      api<{ problems: Problem[] }>('GET', endpoint)
        .then((r) => (problems = r.problems))
        .catch((e) => (error = errorText(codeOf(e))));
    });
  });
</script>

<h1>{t('health.title')}</h1>
<p class="muted">{t('health.intro')}</p>
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if problems}
  <p class="health-summary" role="status">
    {#if problems.length}
      <strong class:sev-error={errors > 0}>{t('health.errors', { n: String(errors) })}</strong> · <span>{t('health.warnings', { n: String(warnings) })}</span>
    {:else}
      {t('health.none')}
    {/if}
  </p>
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
