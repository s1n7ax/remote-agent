import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { initPairing } from './lib/pairing';
import { applyTheme, getTheme } from './lib/theme';

initPairing();
applyTheme(getTheme());
const mock = import.meta.env.VITE_MOCK === '1' || new URLSearchParams(location.search).has('mock');
if (mock) (await import('./lib/mock')).installMock();

if ('serviceWorker' in navigator && import.meta.env.PROD) {
  navigator.serviceWorker.register('/sw.js').catch(() => {
    // the app works without the service worker; push and offline shell are off
  });
}

export default mount(App, { target: document.getElementById('app')! });
