// Primero el modo de color: así <html> ya tiene su data-scheme antes de montar (sin parpadeo).
import './lib/scheme.svelte';
import { mount } from 'svelte';
import App from './App.svelte';
import './lib/fonts';
import './app.css';
import './lib/chroma.css';

mount(App, { target: document.getElementById('app')! });
