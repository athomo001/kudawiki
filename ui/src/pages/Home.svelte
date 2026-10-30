<script lang="ts">
  import type { Me } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';

  // Inicio solo se ve sin áreas: con alguna se entra directo a la wiki (SPEC §15). Las áreas
  // con su rol están en el perfil.
  let { me }: { me: Me } = $props();
</script>

<h1>{t('home.greeting', { name: me.full_name })}</h1>
<p class="muted">{t('home.no_areas')}</p>
