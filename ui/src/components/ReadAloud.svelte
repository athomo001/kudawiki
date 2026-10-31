<script lang="ts">
  import { onDestroy } from 'svelte';
  import Icon from './Icon.svelte';
  import { t } from '../lib/i18n/index.svelte';

  // Lectura en voz alta de la página (SPEC §15). Solo con voces del equipo (localService): las
  // voces "en línea" de Chrome y Edge mandan el texto a Google o a Microsoft, y una página N2 o
  // N3 no sale de la wiki (§3.3). Sin una voz local para el idioma de la página no hay botón.
  let { root, title, lang }: { root: HTMLElement | undefined; title: string; lang: string } = $props();

  type Chunk = { el?: HTMLElement; text: string };

  const BLOCKS = 'h1, h2, h3, h4, h5, h6, p, li, dt, dd, th, td, figcaption';
  // Lo que no se lee dentro de un bloque: listas anidadas (van aparte), fórmulas y adornos.
  const NOT_READ = 'ul, ol, pre, svg, .katex, .heading-anchor, .footnote-backref, .secret-value, [aria-hidden="true"]';

  const synth = 'speechSynthesis' in window ? speechSynthesis : undefined;
  let voices = $state(synth?.getVoices() ?? []);
  // Primero la del idioma y país del navegador (es-CL antes que es-ES), después la predeterminada.
  const voice = $derived.by(() => {
    const local = voices.filter((v) => v.localService && v.lang.toLowerCase().replace('_', '-').split('-')[0] === lang);
    return local.find((v) => v.lang === navigator.language) ?? local.find((v) => v.default) ?? local[0];
  });

  let status = $state<'idle' | 'playing' | 'paused'>('idle');
  let chunks: Chunk[] = [];
  let index = 0;
  let current: SpeechSynthesisUtterance | undefined;
  let marked: HTMLElement | undefined;

  // Chrome carga las voces después de abrir la página.
  $effect(() => {
    if (!synth) return;
    const load = () => (voices = synth.getVoices());
    synth.addEventListener('voiceschanged', load);
    return () => synth.removeEventListener('voiceschanged', load);
  });

  onDestroy(stop);

  // Por frases: una pausa retoma desde la frase en curso, y Chrome corta las locuciones largas.
  function collect(): Chunk[] {
    const out: Chunk[] = [];
    const sentences = new Intl.Segmenter(lang, { granularity: 'sentence' });
    const add = (text: string, el?: HTMLElement) => {
      for (const { segment } of sentences.segment(text)) if (segment.trim()) out.push({ el, text: segment.trim() });
    };
    add(title);
    for (const el of root?.querySelectorAll<HTMLElement>(BLOCKS) ?? []) {
      // Un ítem con párrafos se lee por sus párrafos; lo que no se ve (pestaña cerrada, el
      // título repetido) no se lee; el código y los diagramas tampoco.
      if (el.closest('pre, svg, .mermaid') || el.querySelector('p') || !el.checkVisibility()) continue;
      const copy = el.cloneNode(true) as HTMLElement;
      copy.querySelectorAll(NOT_READ).forEach((n) => n.remove());
      const text = copy.textContent?.replace(/\s+/g, ' ').trim();
      if (text) add(text, el);
    }
    return out;
  }

  function speak(i: number) {
    const chunk = chunks[i];
    if (!synth || !voice || !chunk) return stop();
    index = i;
    mark(chunk.el);
    const u = new SpeechSynthesisUtterance(chunk.text);
    u.voice = voice;
    u.lang = voice.lang;
    // cancel() también dispara end o error: solo avanza la locución que sigue siendo la actual.
    u.onend = () => current === u && speak(i + 1);
    u.onerror = () => current === u && stop();
    current = u;
    synth.speak(u);
  }

  function play() {
    if (status === 'idle') {
      chunks = collect();
      index = 0;
    }
    status = 'playing';
    speak(index);
  }

  // speechSynthesis.pause() falla en Chrome: se corta y se retoma desde la misma frase.
  function pause() {
    status = 'paused';
    current = undefined;
    synth?.cancel();
  }

  function stop() {
    status = 'idle';
    current = undefined;
    synth?.cancel();
    mark(undefined);
  }

  function mark(el: HTMLElement | undefined) {
    if (el === marked) return;
    marked?.classList.remove('reading-now');
    marked = el;
    el?.classList.add('reading-now');
    el?.scrollIntoView({ block: 'nearest' });
  }
</script>

{#if voice}
  {#if status === 'playing'}
    <button type="button" class="action" aria-label={t('tts.pause')} title={t('tts.pause')} onclick={pause}><Icon name="pause" /></button>
  {:else}
    {@const label = status === 'paused' ? t('tts.resume') : t('tts.play')}
    <button type="button" class="action" aria-label={label} title={label} onclick={play}><Icon name="speaker" /></button>
  {/if}
  {#if status !== 'idle'}
    <button type="button" class="action" aria-label={t('tts.stop')} title={t('tts.stop')} onclick={stop}><Icon name="stop" /></button>
  {/if}
{/if}
