import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightBlog from 'starlight-blog';

// Served by GitHub Pages at https://srjn45.github.io/wdx/. A domain of its own
// later changes site and drops base.
//
// The landing page is src/pages/index.astro, a page of its own; Starlight
// serves everything under src/content/docs: for now the blog, at /wdx/blog/.
export default defineConfig({
  site: 'https://srjn45.github.io',
  base: '/wdx',
  trailingSlash: 'ignore',
  integrations: [
    starlight({
      title: 'wdx',
      description: 'Run a team of AI coding agents from one terminal.',
      favicon: '/favicon.svg',
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/srjn45/wdx' }],
      components: { SiteTitle: './src/components/starlight/SiteTitle.astro' },
      customCss: [
        '@fontsource/bricolage-grotesque/500.css',
        '@fontsource/bricolage-grotesque/700.css',
        '@fontsource/bricolage-grotesque/800.css',
        '@fontsource/ibm-plex-sans/400.css',
        '@fontsource/ibm-plex-sans/500.css',
        '@fontsource/ibm-plex-sans/600.css',
        '@fontsource/jetbrains-mono/400.css',
        '@fontsource/jetbrains-mono/700.css',
        './src/styles/tokens.css',
        './src/styles/starlight.css',
      ],
      plugins: [
        starlightBlog({
          title: 'Blog',
          navigation: 'header-end',
          postCount: 10,
          recentPostCount: 8,
          metrics: { readingTime: true },
          authors: {
            srajan: { name: 'Srajan Pathak', url: 'https://github.com/srjn45' },
          },
        }),
      ],
    }),
  ],
});
