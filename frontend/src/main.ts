import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import './assets/main.css'

// Catch Vite dynamic chunk preload errors (e.g. after a new Docker build is deployed)
window.addEventListener('vite:preloadError', (event: any) => {
  event.preventDefault()
  const reloadKey = 'docktun_preload_reload'
  const lastReload = sessionStorage.getItem(reloadKey)
  const now = Date.now()
  if (!lastReload || now - parseInt(lastReload, 10) > 8000) {
    sessionStorage.setItem(reloadKey, now.toString())
    window.location.reload()
  }
})

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
