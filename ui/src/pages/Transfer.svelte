<script lang="ts">
  import PageHead from '../components/PageHead.svelte';
  import { api, codeOf, type Level } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Traslado de páginas entre áreas (SPEC §4.13), solo del superadmin. Primero la vista previa
  // (rutas y niveles en el destino, enlaces que quedan rotos en el origen, quién pierde y gana
  // acceso); trasladar se habilita con una vista previa vigente y sin rutas ocupadas. Se muestran
  // rutas, no títulos: el superadmin no es miembro de las áreas.
  let { context }: { context: string } = $props();

  type AreaRow = { id: string; slug: string; name: string; status: string; delete_at?: string };
  type Plan = { slug: string; new_slug: string; level: Level; new_level: Level };
  type Preview = {
    pages: Plan[];
    sealed: Plan[];
    taken: string[];
    broken_links: { from: string; to: string }[];
    proposals: number;
    shares: number;
    reads: number;
    lose: string[];
    gain: string[];
  };

  let areas = $state<AreaRow[]>([]);
  let form = $state({ from: '', slug: '', dest: '', to: '', level: 0 });
  let preview = $state<Preview | null>(null);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  // Solo las activas: una archivada no entrega ni recibe (§3.5).
  const active = $derived(areas.filter((a) => a.status === 'active' && !a.delete_at));

  $effect(() => {
    api<AreaRow[]>('GET', '/admin/workspaces')
      .then((all) => (areas = all))
      .catch((e) => (error = errorText(codeOf(e))));
  });

  // Cualquier cambio en el pedido invalida la vista previa.
  $effect(() => {
    void [form.from, form.slug, form.dest, form.to, form.level];
    preview = null;
  });

  const body = () => ({ ...form, to: form.to.trim() || form.slug.trim(), slug: form.slug.trim() });

  async function run(path: string, done?: string) {
    busy = true;
    error = '';
    notice = '';
    try {
      const p = await api<Preview>('POST', path, body());
      if (done) {
        notice = done;
        form = { ...form, slug: '', to: '' };
      } else preview = p;
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }
</script>

<PageHead {context} title={t('transfer.title')} description={t('transfer.intro')} />

<form
  class="transfer-form"
  onsubmit={(e) => {
    e.preventDefault();
    run('/admin/transfer/preview');
  }}
>
  <fieldset>
    <legend>{t('transfer.from')}</legend>
    <label class="field"
      >{t('audit.area')}
      <select required bind:value={form.from}>
        <option value="" disabled>{t('transfer.choose_area')}</option>
        {#each active as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
      </select>
    </label>
    <label class="field">{t('transfer.path')} <input class="mono" required placeholder="redes/vpn" bind:value={form.slug} /></label>
  </fieldset>
  <fieldset>
    <legend>{t('transfer.dest')}</legend>
    <label class="field"
      >{t('audit.area')}
      <select required bind:value={form.dest}>
        <option value="" disabled>{t('transfer.choose_area')}</option>
        {#each active.filter((a) => a.id !== form.from) as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
      </select>
    </label>
    <label class="field"
      >{t('transfer.new_path')} <input class="mono" placeholder={form.slug || 'infra/vpn'} bind:value={form.to} />
      <small class="muted">{t('transfer.new_path_hint')}</small></label
    >
    <label class="field"
      >{t('page.level')}
      <select bind:value={form.level}>
        <option value={0}>{t('transfer.keep_level')}</option>
        {#each [1, 2, 3] as const as l (l)}<option value={l}>{t(`level.${l}`)}</option>{/each}
      </select>
    </label>
  </fieldset>
  <p class="form-actions"><button type="submit" class="secondary" disabled={busy}>{t('transfer.preview')}</button></p>
</form>

{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if notice}<p class="notice" role="status">{notice}</p>{/if}

{#if preview}
  <h2>{t('transfer.preview_title')}</h2>
  {#if preview.taken.length}
    <p class="callout callout-danger" role="alert">{t('transfer.taken', { paths: preview.taken.join(', ') })}</p>
  {/if}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('transfer.path')}</th>
        <th scope="col">{t('transfer.new_path')}</th>
        <th scope="col">{t('page.level')}</th>
      </tr>
    </thead>
    <tbody>
      {#each preview.pages as p (p.slug)}
        <tr>
          <td class="mono">{p.slug}</td>
          <td class="mono">{p.new_slug}</td>
          <td>{#if p.level === p.new_level}{t(`level.${p.new_level}`)}{:else}{t(`level.${p.level}`)} → <strong>{t(`level.${p.new_level}`)}</strong>{/if}</td>
        </tr>
      {/each}
      {#each preview.sealed as s (s.slug)}
        <tr>
          <td class="mono">{s.slug}</td>
          <td class="mono">{s.new_slug}</td>
          <td>{t('transfer.sealed')}</td>
        </tr>
      {/each}
    </tbody>
  </table>
  <dl class="facts">
    <dt>{t('transfer.lose')}</dt>
    <dd>{preview.lose.join(', ') || '—'}</dd>
    <dt>{t('transfer.gain')}</dt>
    <dd>{preview.gain.join(', ') || '—'}</dd>
    <dt>{t('transfer.closes')}</dt>
    <dd>{t('transfer.closes_detail', { proposals: String(preview.proposals), shares: String(preview.shares), reads: String(preview.reads) })}</dd>
  </dl>
  {#if preview.broken_links.length}
    <h3>{t('transfer.broken_links')}</h3>
    <ul>
      {#each preview.broken_links as l (l.from + l.to)}<li><span class="mono">{l.from}</span> → <span class="mono">{l.to}</span></li>{/each}
    </ul>
  {/if}
  <p class="muted">{t('transfer.no_redirect')}</p>
  <p class="form-actions">
    <button type="button" class="danger-btn" disabled={busy || preview.taken.length > 0} onclick={() => run('/admin/transfer', t('transfer.done'))}
      >{t('transfer.confirm', { n: String(preview.pages.length + preview.sealed.length) })}</button
    >
  </p>
{/if}
