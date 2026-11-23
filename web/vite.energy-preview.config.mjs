import { mergeConfig } from 'vite';
import config from './vite.config.ts';

export default mergeConfig(config, {
  define: { 'import.meta.env.VITE_ENERGY_PREVIEW': JSON.stringify('1') },
  server: {
    host: '127.0.0.1',
    port: 5176,
    strictPort: true,
    proxy: { '/admin': 'http://127.0.0.1:18089' },
  },
});
