<script lang="ts">
  import { t } from '../lib/i18n/index.svelte';

  // Guía de Markdown del editor (SPEC §15): qué se escribe y qué se obtiene, con las extensiones
  // de Kudawiki (§4.10), y los atajos. "Insertar" deja el ejemplo donde está el cursor; los
  // bloques van en su propia línea.
  let { oninsert }: { oninsert: (text: string, block: boolean) => void } = $props();

  const groups = [
    { key: 'text', rows: ['bold', 'italic', 'strike', 'code', 'kbd', 'math', 'secret'], block: false },
    { key: 'structure', rows: ['h2', 'h3', 'ul', 'ol', 'task', 'quote', 'table', 'codeblock', 'hr'], block: true },
    { key: 'links', rows: ['link', 'wikilink', 'wikilink_text', 'image', 'image_size', 'image_center', 'image_left', 'image_row'], block: false },
    { key: 'blocks', rows: ['callout', 'tabs', 'steps', 'details', 'secret_block', 'toc', 'children', 'footnote', 'mermaid'], block: true },
  ] as const;

  const keys = [
    ['Ctrl+S', 'save'],
    ['Ctrl+B', 'bold'],
    ['Ctrl+I', 'italic'],
    ['Ctrl+Z', 'undo'],
    ['Ctrl+Shift+Z', 'redo'],
    ['Ctrl+F', 'find'],
    ['Ctrl+K', 'search'],
    ['E', 'edit'],
    ['G H', 'history'],
  ] as const;
</script>

<div class="md-guide">
  <p class="muted">{t('mdguide.intro')}</p>
  {#each groups as g (g.key)}
    <h3>{t(`mdguide.group.${g.key}`)}</h3>
    <table>
      <thead>
        <tr>
          <th scope="col">{t('mdguide.you_write')}</th>
          <th scope="col">{t('mdguide.you_get')}</th>
          <th scope="col"><span class="sr-only">{t('mdguide.insert')}</span></th>
        </tr>
      </thead>
      <tbody>
        {#each g.rows as row (row)}
          {@const code = t(`mdguide.${row}.code`)}
          <tr>
            <td><pre>{code}</pre></td>
            <td>{t(`mdguide.${row}.desc`)}</td>
            <td>
              <button type="button" class="link" aria-label={t('mdguide.insert_label', { what: t(`mdguide.${row}.desc`) })} onclick={() => oninsert(code, g.block)}>
                {t('mdguide.insert')}
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/each}

  <h3>{t('mdguide.group.keys')}</h3>
  <dl class="md-keys">
    {#each keys as [combo, what] (combo)}
      <dt>{#each combo.split(/([+ ])/) as part, i (i)}{#if part === '+' || part === ' '}{part}{:else}<kbd>{part}</kbd>{/if}{/each}</dt>
      <dd>{t(`mdguide.key.${what}`)}</dd>
    {/each}
  </dl>
  <p class="muted small">{t('mdguide.keys_note')}</p>
</div>
