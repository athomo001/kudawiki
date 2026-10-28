// Corre dentro del iframe aislado (sandbox="allow-scripts", origen opaco): no tiene acceso a
// la sesión, a la API ni al DOM de la wiki. Solo recibe el texto del diagrama y devuelve el alto.
import mermaid from 'mermaid';

const out = document.getElementById('out')!;

addEventListener('message', async (e: MessageEvent) => {
  if (e.source !== parent || e.data?.type !== 'render' || typeof e.data.source !== 'string') return;
  // securityLevel 'strict': Mermaid escapa las etiquetas y no permite clics ni HTML en el diagrama.
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: e.data.dark ? 'dark' : 'default' });
  try {
    const { svg } = await mermaid.render('d', e.data.source);
    out.innerHTML = svg;
  } catch {
    out.textContent = e.data.source;
  }
  parent.postMessage({ type: 'size', height: document.documentElement.scrollHeight }, '*');
});

parent.postMessage({ type: 'ready' }, '*');
