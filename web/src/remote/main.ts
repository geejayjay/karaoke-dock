import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import '../shared/tailwind.css'

export const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 30_000, retry: 1, refetchOnWindowFocus: false } },
})

// Per room, so an icon on the home screen opens this party.
const manifest = document.createElement('link')
manifest.rel = 'manifest'
manifest.href = `/r/${location.pathname.split('/')[2] ?? ''}/manifest.webmanifest`
document.head.appendChild(manifest)

createApp(App).use(createPinia()).use(VueQueryPlugin, { queryClient }).mount('#app')
