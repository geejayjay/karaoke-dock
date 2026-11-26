import { createApp } from 'vue'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import '../shared/tailwind.css'

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 5_000, retry: false, refetchOnWindowFocus: false } },
})

createApp(App).use(VueQueryPlugin, { queryClient }).mount('#app')
