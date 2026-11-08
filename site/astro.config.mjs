import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightBlog from 'starlight-blog';
import starlightLinksValidator from 'starlight-links-validator';

// Served by GitHub Pages at https://srjn45.github.io/wdx/. A domain of its own
// later changes site and drops base.
//
// The landing page is src/pages/index.astro, a page of its own; Starlight
// serves everything under src/content/docs: the docs at /wdx/docs/ and the
// blog at /wdx/blog/. A broken link inside the site fails the build.
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
      sidebar: [
        { label: 'Overview', link: '/docs/' },
        {
          label: 'Start',
          items: ['docs/start/what-is-wdx', 'docs/start/install', 'docs/start/quickstart', 'docs/start/concepts'],
        },
        {
          label: 'Guides',
          items: [
            'docs/guides/projects',
            'docs/guides/terminals',
            'docs/guides/agents',
            'docs/guides/child-agents',
            'docs/guides/messages',
            'docs/guides/worktrees',
          ],
        },
        {
          label: 'Running wdx',
          items: [
            'docs/guides/daemon',
            'docs/guides/settings',
            'docs/guides/updating',
            'docs/guides/dependencies',
            'docs/guides/observability',
            'docs/guides/completion',
          ],
        },
        {
          label: 'Reference',
          collapsed: true,
          items: [
            { label: 'CLI', collapsed: true, items: [{ autogenerate: { directory: 'docs/reference/cli' } }] },
            'docs/reference/settings',
            'docs/reference/errors',
            'docs/reference/json',
            'docs/reference/changelog',
          ],
        },
      ],
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
        // The landing page and the feed are not Starlight's pages, so it cannot check them.
        starlightLinksValidator({ exclude: ['/wdx/', '/wdx/#*', '/wdx/blog/rss.xml'] }),
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
