import { defineConfig } from '@hey-api/openapi-ts';

export default defineConfig({
  input: '../api/openapi/management-runtime-v1.json',
  output: 'src/client',
  plugins: ['@hey-api/client-fetch'],
});
