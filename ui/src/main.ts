import { mount } from 'svelte';
import App from './App.svelte';
import './lib/fonts';
import './app.css';
import './lib/chroma.css';

mount(App, { target: document.getElementById('app')! });
