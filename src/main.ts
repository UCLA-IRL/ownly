/// <reference types="vite-plugin-pwa/client" />

import { createApp } from 'vue';
import App from '@/App.vue';
import router from '@/router';

import '@/assets/main.scss';
import 'vue-virtual-scroller/dist/vue-virtual-scroller.css';
import 'vue3-toastify/dist/index.css';

// https://vite-pwa-org.netlify.app/guide/auto-update
import { registerSW } from 'virtual:pwa-register';
registerSW({ immediate: true });

// Initialize browser services
import { IDBStatsDb } from '@/services/database/stats_browser';
import { IDBProjDb } from './services/database/proj_db_browser';
import { IDBBootStateDb } from './services/database/boot_db_browser';
import streamSaver from 'streamsaver';

import Bugsnag from '@bugsnag/js'
import BugsnagPluginVue from '@bugsnag/plugin-vue'
import BugsnagPerformance from '@bugsnag/browser-performance'

globalThis._o = {
  stats: new IDBStatsDb(),
  ProjDb: IDBProjDb,
  bootState: new IDBBootStateDb(),

  getStorageRoot: () => window.navigator.storage.getDirectory(),
  streamSaver: streamSaver,
};

Bugsnag.start({
  apiKey: '76a3c6d0b791072ef5bf22b5864600b5',
  plugins: [new BugsnagPluginVue()]
})
BugsnagPerformance.start({ apiKey: '76a3c6d0b791072ef5bf22b5864600b5' })

const bugsnagVue = Bugsnag.getPlugin('vue')

// Initialize Vue app
const app = createApp(App);
app.use(bugsnagVue!)
app.use(router);
app.mount('#app');
Bugsnag.notify("test error")
