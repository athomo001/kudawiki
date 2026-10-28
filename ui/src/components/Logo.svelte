<script lang="ts">
  // Logo D · Capas (spec/logo/, SPEC §15). Los colores salen de variables CSS: sobre la barra
  // oscura es la versión para fondo oscuro, y en el resto sigue el tema, como el favicon. Lo
  // reemplaza el de la empresa cuando lo sube (§15.1). Decorativo: el nombre va al lado.
  let { size = 26 }: { size?: number } = $props();
</script>

<svg class="logo" width={size} height={size} viewBox="0 0 48 48" aria-hidden="true">
  <rect class="logo-back" x="15" y="3" width="30" height="30" rx="4" />
  <rect class="logo-mid" x="9" y="9" width="30" height="30" rx="4" />
  <rect class="logo-front" x="3" y="15" width="30" height="30" rx="4" />
  <path class="logo-k" d="M13.5 22v16M13.5 31l8-9M16 28.5l6.5 9.5" />
</svg>
