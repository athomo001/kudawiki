<script lang="ts">
  import type { Area } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';
  import FileBrowser from '../components/FileBrowser.svelte';

  // Archivos del área (SPEC §4.4), para editores y admins: ver lo que se subió, copiar su enlace y
  // borrar lo que ya nadie usa.
  let { area }: { area: Area } = $props();
</script>

<h1>{t('files.title')}</h1>
<p class="muted">{t('files.intro')}</p>
<FileBrowser {area} />
