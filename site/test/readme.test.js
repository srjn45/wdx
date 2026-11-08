// The README is the front page on GitHub, outside the site's build: these
// keep what it shows the same as the site, so it cannot go stale unseen.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { installCommand } from '../src/release.js';

const readme = readFileSync(new URL('../../README.md', import.meta.url), 'utf8');
const docs = new URL('../src/content/docs/docs/', import.meta.url);

// The lines of the README's first sh block after the heading named.
function block(heading) {
  const after = readme.split(`## ${heading}\n`)[1];
  assert.ok(after, `the README has no ${heading} section`);
  return after.split('```sh\n')[1].split('```')[0].trim().split('\n');
}

test('the README installs with the command the site shows', () => {
  assert.deepEqual(block('Install'), [installCommand]);
});

test("every command of the README's quick start is in the docs' Quickstart", () => {
  const quickstart = readFileSync(new URL('start/quickstart.md', docs), 'utf8')
    .split('\n').map((line) => line.replace(/^\$ /, '').trim());
  for (const line of block('Quick start').filter((l) => l.startsWith('wdx '))) {
    assert.ok(quickstart.includes(line), `not in the Quickstart: ${line}`);
  }
});

test("the README's links into the docs name pages that exist", () => {
  const links = [...readme.matchAll(/https:\/\/srjn45\.github\.io\/wdx\/docs\/([^)"#\s]*)/g)].map((m) => m[1].replace(/\/$/, ''));
  assert.ok(links.length > 10, 'the README links the docs');
  for (const path of links) {
    const page = path === '' ? 'index' : path;
    const found = ['.md', '.mdx', '/index.md', '/index.mdx'].some((ext) => existsSync(new URL(page + ext, docs)));
    assert.ok(found, `no docs page for /wdx/docs/${path}/`);
  }
});
