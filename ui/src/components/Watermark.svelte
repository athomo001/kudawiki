<script lang="ts">
  import { site, logoURL } from '../lib/site.svelte';
  import { viewer } from '../lib/watermark.svelte';
  import { locale } from '../lib/i18n/index.svelte';

  // Marca de agua (§4.15): capa tenue y repetida con el logo y el nombre de la empresa, y el nombre,
  // el correo y la hora de quien lee, para que una captura filtrada diga de dónde salió. Es un SVG
  // fijo sobre la pantalla: se ve igual al desplazarse y se repite en cada hoja impresa. No se
  // guarda en el contenido y no bloquea seleccionar el texto: es disuasión, no DRM.
  const id = $props.id();
  let now = $state(new Date());
  // La hora sigue a la del reloj: una captura de una pestaña abierta hace horas dice cuándo se hizo.
  $effect(() => {
    const timer = setInterval(() => (now = new Date()), 60_000);
    return () => clearInterval(timer);
  });
  const stamp = $derived(new Intl.DateTimeFormat(locale(), { dateStyle: 'medium', timeStyle: 'short' }).format(now));
  const logo = $derived(logoURL(false));
</script>

<svg class="watermark" aria-hidden="true" data-testid="watermark">
  <defs>
    <!-- El logo de la empresa en un solo tono, como pide §15.1.4: en color distraería del texto. -->
    <filter id={`${id}-mono`}><feColorMatrix type="saturate" values="0" /></filter>
    <pattern id={`${id}-tile`} width="440" height="200" patternUnits="userSpaceOnUse" patternTransform="rotate(-24)">
      {#if logo}<image href={logo} x="16" y="24" width="120" height="22" preserveAspectRatio="xMinYMid meet" filter={`url(#${id}-mono)`} />{/if}
      <text x="16" y={logo ? 66 : 44} class="watermark-company">{site.company || site.title}</text>
      <text x="16" y={logo ? 88 : 66}>{viewer.name} · {viewer.email}</text>
      <text x="16" y={logo ? 108 : 86}>{stamp}</text>
    </pattern>
  </defs>
  <rect width="100%" height="100%" fill={`url(#${id}-tile)`} />
</svg>
