import { randomBytes } from 'node:crypto';
import type { Plugin } from 'vite';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const buildVersion = process.env.CLUSTERFORGE_BUILD_VERSION?.trim()
  || `${Date.now().toString(36)}-${randomBytes(4).toString('hex')}`;

function buildVersionPlugin(version: string): Plugin {
  const payload = `${JSON.stringify({ version })}\n`;
  return {
    name: 'clusterforge-build-version',
    transformIndexHtml: {
      order: 'pre',
      handler() {
        return [{
          tag: 'meta',
          attrs: { name: 'clusterforge-build-version', content: version },
          injectTo: 'head',
        }];
      },
    },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        if (request.url?.split('?', 1)[0] !== '/version.json') {
          next();
          return;
        }
        response.statusCode = 200;
        response.setHeader('Content-Type', 'application/json; charset=utf-8');
        response.setHeader('Cache-Control', 'no-store');
        response.end(payload);
      });
    },
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: 'version.json', source: payload });
    },
  };
}

export default defineConfig({
  plugins: [buildVersionPlugin(buildVersion), react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_PROXY_TARGET ?? 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
});
