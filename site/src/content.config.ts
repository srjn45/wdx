import { defineCollection } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { blogSchema } from 'starlight-blog/schema';

// Starlight's pages, blog posts among them: a post's frontmatter adds date,
// excerpt, tags and authors to Starlight's own (title, draft, ...).
export const collections = {
  docs: defineCollection({ loader: docsLoader(), schema: docsSchema({ extend: (context) => blogSchema(context) }) }),
};
