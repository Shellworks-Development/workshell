import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // The Go server embeds this directory into its binary.
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
  server: {
    // Listen on all interfaces so phones on the LAN can open the dev server by IP.
    host: true,
    proxy: {
      '/api': process.env.WORKSHELL_API ?? 'http://localhost:8080',
    },
  },
  preview: { host: true },
})
