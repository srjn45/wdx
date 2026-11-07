import { defineConfig } from 'astro/config';

// Served by GitHub Pages at https://srjn45.github.io/wdx/. A domain of its own
// later changes site and drops base.
export default defineConfig({
  site: 'https://srjn45.github.io',
  base: '/wdx',
  trailingSlash: 'ignore',
});
