<script lang="ts">
  import { api, ApiError, canEdit, canWrite, codeOf, type Me, type ReadingPrefs } from './lib/api';
  import { setLocale, errorText } from './lib/i18n/index.svelte';
  import { applyPrefs } from './lib/prefs';
  import { navigate, route } from './lib/router.svelte';
  import TopBar from './components/TopBar.svelte';
  import Login from './Login.svelte';
  import Home from './pages/Home.svelte';
  import Profile from './pages/Profile.svelte';
  import Admin from './pages/Admin.svelte';
  import Members from './pages/Members.svelte';
  import Audit from './pages/Audit.svelte';
  import Health from './pages/Health.svelte';
  import Appearance from './pages/Appearance.svelte';
  import Email from './pages/Email.svelte';
  import SiteFooter from './components/SiteFooter.svelte';
  import Area from './pages/Area.svelte';
  import Invite from './pages/Invite.svelte';
  import Forgot from './pages/Forgot.svelte';
  import Reset from './pages/Reset.svelte';
  import Unsubscribe from './pages/Unsubscribe.svelte';
  import NotFound from './pages/NotFound.svelte';

  let me = $state<Me | null>(null);
  let ready = $state(false);
  let error = $state('');
  let avatarRev = $state(0);

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

  // El perfil cambió el nombre o la foto: se vuelve a leer y la foto se pide con otra URL.
  async function profileChanged() {
    await loadMe();
    avatarRev++;
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
  // Igual que en Area.svelte: un lector abre el editor solo para proponer (§19.5).
  const editing = $derived.by(() => {
    const sub = areaPath?.[2] ?? '';
    if (!area) return false;
    return (sub === 'new' && canEdit(area)) || (sub.startsWith('edit/') && canWrite(area)) || /^proposals\/[^/]+\/edit$/.test(sub);
  });

  // Al entrar se cae directo en la wiki (SPEC §15): en la última área abierta en este navegador,
  // si la persona sigue siendo miembro, o en la primera. Inicio queda solo para quien no tiene
  // áreas. El identificador del área ya está en el historial del navegador: guardarlo no agrega nada.
  const LAST_AREA_KEY = 'kw.last_area';
  $effect(() => {
    if (!me || route.path !== '/') return;
    let last: string | null = null;
    try {
      last = localStorage.getItem(LAST_AREA_KEY);
    } catch {
      // sin almacenamiento (modo privado estricto): la primera área
    }
    const to = me.workspaces.find((w) => w.slug === last) ?? me.workspaces[0];
    if (to) navigate(`/w/${to.slug}`, { replace: true });
  });
  $effect(() => {
    if (!area) return;
    try {
      localStorage.setItem(LAST_AREA_KEY, area.slug);
    } catch {
      // ídem: solo se pierde el recuerdo
    }
  });

  loadMe();
</script>

{#if route.path === '/invite'}
  <Invite />
  <SiteFooter />
{:else if route.path === '/reset'}
  <Reset />
  <SiteFooter />
{:else if route.path === '/unsubscribe'}
  <Unsubscribe />
  <SiteFooter />
{:else if route.path === '/forgot'}
  <Forgot />
  <SiteFooter />
{:else if ready}
  {#if me}
    {#if !editing}<TopBar {me} {area} {avatarRev} onprefs={savePrefs} onlogout={logout} />{/if}
    {#if error}<p class="error banner" role="alert">{error}</p>{/if}
    {#if area}
      <Area {area} sub={areaPath?.[2] ?? ''} selfId={me.id} onareachanged={loadMe} />
    {:else}
      <main class="view">
        {#if route.path === '/'}
          {#if !me.workspaces.length}<Home {me} />{/if}
        {:else if route.path === '/me/profile'}
          <Profile {me} {avatarRev} onprefs={savePrefs} onchanged={profileChanged} />
        {:else if route.path === '/admin' && me.is_global_admin}
          <Admin onchanged={loadMe} />
        {:else if route.path === '/admin/audit' && me.is_global_admin}
          <Audit endpoint="/admin/audit" global />
        {:else if route.path === '/admin/health' && me.is_global_admin}
          <Health endpoint="/admin/health" global />
        {:else if route.path === '/admin/appearance' && me.is_global_admin}
          <Appearance />
        {:else if route.path === '/admin/email' && me.is_global_admin}
          <Email email={me.email} />
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
