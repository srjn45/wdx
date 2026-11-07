// Checks the built site in dist/ (npm run build first): every post that is not
// a draft has its page and an item in the feed, and the landing page links to
// the newest posts.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readdirSync, readFileSync } from 'node:fs';

const dist = new URL('../../dist/', import.meta.url);
const postsDir = new URL('../../src/content/docs/blog/', import.meta.url);
const read = (path) => readFileSync(new URL(path, dist), 'utf8');

const posts = readdirSync(postsDir)
  .filter((f) => /\.mdx?$/.test(f))
  .map((f) => {
    const front = readFileSync(new URL(f, postsDir), 'utf8').split('---')[1];
    return { slug: f.replace(/\.mdx?$/, ''), title: front.match(/^title: (.*)$/m)[1], draft: /^draft: true$/m.test(front) };
  });
const published = posts.filter((p) => !p.draft);

test('the site is built', () => {
  assert.ok(existsSync(new URL('index.html', dist)), 'no dist/index.html: run npm run build first');
});

test('every published post has its page', () => {
  for (const p of published) assert.ok(existsSync(new URL(`blog/${p.slug}/index.html`, dist)), p.slug);
});

test('drafts are not built', () => {
  for (const p of posts.filter((p) => p.draft)) assert.ok(!existsSync(new URL(`blog/${p.slug}/index.html`, dist)), p.slug);
});

test('the feed lists every published post, linked to its page', () => {
  const rss = read('blog/rss.xml');
  assert.match(rss, /^<\?xml/);
  const items = rss.match(/<item>[\s\S]*?<\/item>/g) || [];
  assert.equal(items.length, published.length);
  for (const p of published) {
    assert.ok(items.some((i) => i.includes(`<link>https://srjn45.github.io/wdx/blog/${p.slug}/</link>`)), p.slug);
  }
});

test('the landing page links to the newest posts and names the feed', () => {
  const html = read('index.html');
  assert.match(html, /<link rel="alternate" type="application\/rss\+xml"[^>]*href="https:\/\/srjn45\.github\.io\/wdx\/blog\/rss\.xml"/);
  const linked = [...html.matchAll(/class="post" href="\/wdx\/blog\/([^/"]+)\/"/g)].map((m) => m[1]);
  assert.equal(linked.length, Math.min(3, published.length));
  for (const slug of linked) assert.ok(published.some((p) => p.slug === slug), slug);
});
