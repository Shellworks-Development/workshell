import { createApp } from 'vue'
import App from './App.vue'
import { registerServiceWorker } from './lib/pwa'
import { router } from './router'
import './style.css'

createApp(App).use(router).mount('#app')
registerServiceWorker()
