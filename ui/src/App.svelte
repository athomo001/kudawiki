<script lang="ts">
  import { api, ApiError, canEdit, codeOf, type Me, type ReadingPrefs } from './lib/api';
  import { setLocale, errorText } from './lib/i18n/index.svelte';
  import { applyPrefs } from './lib/prefs';
  import { route } from './lib/router.svelte';
  import TopBar from './components/TopBar.svelte';
  import Login from './Login.svelte';
  import Home from './pages/Home.svelte';
  import Admin from './pages/Admin.svelte';
  import Members from './pages/Members.svelte';
  import Audit from './pages/Audit.svelte';
  import Health from './pages/Health.svelte';
  import Appearance from './pages/Appearance.svelte';
  import SiteFooter from './components/SiteFooter.svelte';
  import Area from './pages/Area.svelte';
  import Invite from './pages/Invite.svelte';
  import NotFound from './pages/NotFound.svelte';

  let me = $state<Me | null>(null);
  let ready = $state(false);
  let error = $state('');

  // El idioma y la lectura siguen al perfil; sin sesión, el navegador y la fuente por defecto.
  $effect(() => {
    if (me) setLocale(me.locale);
    applyPrefs(me?.reading_prefs);
  });

  async function loadMe() {
    error = '';
    try {
      me = await api<Me>('GET', '/auth/me');
    } catch (e) {
      me = null;
      // 401 es el estado normal sin sesión; cualquier otra cosa se muestra.
      if (!(e instanceof ApiError && e.status === 401)) error = errorText(codeOf(e));
    } finally {
      ready = true;
    }
  }

  async function logout() {
    try {
      await api('POST', '/auth/logout');
      me = null;
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // El cambio se ve al instante, sin recargar (SPEC §15); si el servidor lo rechaza, se deshace.
  async function savePrefs(change: { locale?: 'es' | 'en'; reading_prefs?: ReadingPrefs }) {
    if (!me) return;
    const before = { locale: me.locale, reading_prefs: me.reading_prefs };
    Object.assign(me, change);
    error = '';
    try {
      await api('PATCH', '/me', change);
    } catch (e) {
      if (me) Object.assign(me, before);
      error = errorText(codeOf(e));
    }
  }

  // Lo que no calza o no corresponde muestra el mismo "no encontrado" (SPEC §3.3).
  const areaPath = $derived(route.path.match(/^\/w\/([a-z0-9-]+)(?:\/(.*))?$/));
  const adminAreaId = $derived(route.path.match(/^\/admin\/areas\/([0-9A-Z]{26})$/)?.[1]);
  const area = $derived(me?.workspaces.find((w) => w.slug === areaPath?.[1]));
  // El editor tiene su propia cabecera (diseño aprobado, SPEC §15): mientras se edita no hay barra superior.
  const editing = $derived(!!area && canEdit(area) && /^(new$|edit\/)/.test(areaPath?.[2] ?? ''));

  loadMe();
</script>

{#if route.path === '/invite'}
  <Invite />
  <SiteFooter />
{:else if ready}
  {#if me}
    {#if !editing}<TopBar {me} {area} onprefs={savePrefs} onlogout={logout} />{/if}
    {#if error}<p class="error banner" role="alert">{error}</p>{/if}
    {#if area}
      <Area {area} sub={areaPath?.[2] ?? ''} selfId={me.id} onareachanged={loadMe} />
    {:else}
      <main class="view">
        {#if route.path === '/'}
          <Home {me} />
        {:else if route.path === '/admin' && me.is_global_admin}
          <Admin />
        {:else if route.path === '/admin/audit' && me.is_global_admin}
          <Audit endpoint="/admin/audit" global />
        {:else if route.path === '/admin/health' && me.is_global_admin}
          <Health endpoint="/admin/health" global />
        {:else if route.path === '/admin/appearance' && me.is_global_admin}
          <Appearance />
        {:else if adminAreaId && me.is_global_admin}
          <Members endpoint={`/admin/workspaces/${adminAreaId}/members`} canGrantAdmin selfId={me.id} />
        {:else}
          <NotFound />
        {/if}
      </main>
      <SiteFooter />
    {/if}
  {:else}
    <Login onlogin={loadMe} />
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <SiteFooter />
  {/if}
{/if}
