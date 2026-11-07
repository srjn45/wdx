import { test } from 'node:test';
import assert from 'node:assert/strict';
import { latestRelease } from '../src/release.js';

test('the latest release is a stable version from the tags', () => {
  assert.match(latestRelease(), /^\d+\.\d+\.\d+$/);
});
