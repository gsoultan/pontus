import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    TanStackRouterVite(),
  ],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('@mantine/core')) {
              return 'vendor-mantine-core';
            }
            if (id.includes('@mantine/charts') || id.includes('recharts')) {
              return 'vendor-charts';
            }
            if (id.includes('@mantine')) {
              return 'vendor-mantine-misc';
            }
            if (id.includes('@tabler/icons-react')) {
              return 'vendor-icons';
            }
            if (id.includes('@tanstack')) {
              return 'vendor-tanstack';
            }
            if (id.includes('@xyflow')) {
              return 'vendor-xyflow';
            }
            if (id.includes('@bufbuild') || id.includes('@connectrpc')) {
              return 'vendor-rpc';
            }
            if (id.includes('node_modules/react/') || id.includes('node_modules/react-dom/') || id.includes('node_modules/scheduler/')) {
              return 'vendor-react';
            }
            if (id.includes('framer-motion')) {
              return 'vendor-framer';
            }
            return 'vendor-base';
          }
        },
      },
    },
  },
  server: {
    proxy: {
      '/api.proto.ManagementService': {
        target: 'http://localhost:9090',
        changeOrigin: true,
      },
    },
  },
})
