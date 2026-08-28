import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import AccountsView from './views/AccountsView.vue'
import EventsView from './views/EventsView.vue'
import SettingsView from './views/SettingsView.vue'
import './styles.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/accounts' },
    { path: '/accounts', component: AccountsView },
    { path: '/events', component: EventsView },
    { path: '/settings', component: SettingsView },
  ],
})

createApp(App).use(router).mount('#app')
