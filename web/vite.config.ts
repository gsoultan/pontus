import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'
import { VitePWA } from 'vite-plugin-pwa'
import { compression } from 'vite-plugin-compression2'

/** ConnectRPC prefix served by the Go mux. The `.service` segment is load-bearing. */
const RPC_PREFIX = '/api.proto.service.ManagementService'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    TanStackRouterVite(),

    VitePWA({
      registerType: 'autoUpdate',
      injectRegister: null, // registered explicitly in src/pwa/registerServiceWorker.ts
      includeAssets: ['favicon.svg', 'icons.svg'],
      manifest: {
        name: 'Pontus — Database Proxy Control Plane',
        short_name: 'Pontus',
        description:
          'Connection pooling, proxying and load balancing for PostgreSQL and MySQL.',
        theme_color: '#4263eb',
        background_color: '#101113',
        display: 'standalone',
        orientation: 'any',
        start_url: '/',
        scope: '/',
        categories: ['developer', 'productivity', 'utilities'],
        icons: [
          { src: '/favicon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
          { src: '/favicon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'maskable' },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,woff2}'],
        // A 3 MB budget covers the charting and topology vendor chunks.
        maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
        cleanupOutdatedCaches: true,
        clientsClaim: true,
        skipWaiting: false, // the user decides when to reload — see PwaUpdatePrompt
        navigateFallback: '/index.html',
        // The control plane is authenticated and mutable. Never let the SPA
        // fallback or any cache stand in front of an RPC, /metrics, or a stream:
        // a cached management response is a cross-session data leak.
        navigateFallbackDenylist: [new RegExp(`^${RPC_PREFIX}`), /^\/metrics/],
        runtimeCaching: [
          {
            urlPattern: ({ request }) => request.destination === 'font',
            handler: 'CacheFirst',
            options: {
              cacheName: 'pontus-fonts',
              expiration: { maxEntries: 16, maxAgeSeconds: 60 * 60 * 24 * 365 },
            },
          },
          {
            urlPattern: ({ request }) => request.destination === 'image',
            handler: 'StaleWhileRevalidate',
            options: {
              cacheName: 'pontus-images',
              expiration: { maxEntries: 64, maxAgeSeconds: 60 * 60 * 24 * 30 },
            },
          },
        ],
      },
      devOptions: { enabled: false },
    }),

    // Precompress every emitted asset. The Go handler negotiates and serves
    // these directly, so the runtime never spends CPU compressing per request.
    compression({ algorithms: ['brotliCompress'], exclude: [/\.(br|gz)$/], threshold: 1024 }),
    compression({ algorithms: ['gzip'], exclude: [/\.(br|gz)$/], threshold: 1024 }),
  ],

  worker: { format: 'es' },

  build: {
    target: 'es2023',
    cssCodeSplit: true,
    sourcemap: false,
    reportCompressedSize: false,
    chunkSizeWarningLimit: 900,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (id.includes('@mantine/core')) return 'vendor-mantine-core'
          if (id.includes('@mantine/charts') || id.includes('recharts')) return 'vendor-charts'
          if (id.includes('@mantine')) return 'vendor-mantine-misc'
          if (id.includes('@tabler/icons-react')) return 'vendor-icons'
          if (id.includes('@tanstack')) return 'vendor-tanstack'
          if (id.includes('@xyflow')) return 'vendor-xyflow'
          if (id.includes('@bufbuild') || id.includes('@connectrpc')) return 'vendor-rpc'
          if (
            id.includes('node_modules/react/') ||
            id.includes('node_modules/react-dom/') ||
            id.includes('node_modules/scheduler/')
          ) {
            return 'vendor-react'
          }
          if (id.includes('motion')) return 'vendor-motion'
          return 'vendor-base'
        },
      },
    },
  },

  server: {
    proxy: {
      // Must match ManagementServiceName exactly — the `.service` segment is
      // load-bearing. Without it the mux falls through to the SPA handler and
      // every dev-mode RPC silently returns HTML instead of a Connect response.
      [RPC_PREFIX]: {
        target: 'http://localhost:9090',
        changeOrigin: true,
      },
    },
  },
})
