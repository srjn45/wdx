import { defineCollection } from 'astro:content';
import { z } from 'astro/zod';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';
import { blogSchema } from 'starlight-blog/schema';

// Starlight's pages, blog posts among them: a post's frontmatter adds date,
// excerpt, tags and authors to Starlight's own (title, draft, ...). A guide's
// adds commands, the wdx commands it is about, named without wdx
// (agent create): <Commands /> shows them and TestGuideCoverage checks them.
export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({
      extend: (context) => blogSchema(context).extend({ commands: z.array(z.string()).optional() }),
    }),
  }),
};
