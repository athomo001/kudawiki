<script lang="ts">
  import type { Me } from '../lib/api';

  // Foto de perfil o, sin foto, las iniciales (SPEC §8.6.3). rev sube cada vez que la foto
  // cambia: con otra URL el navegador no muestra la que ya tenía en memoria.
  let { me, rev = 0, large = false }: { me: Me; rev?: number; large?: boolean } = $props();

  const initials = $derived(
    me.full_name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w.charAt(0).toUpperCase())
      .join(''),
  );
</script>

{#if me.has_avatar}
  <img class={['avatar', large && 'large']} src={`/api/me/avatar?v=${rev}`} alt="" />
{:else}
  <span class={['avatar', large && 'large']}>{initials}</span>
{/if}
