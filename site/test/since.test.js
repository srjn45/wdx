import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sinceLabel } from '../src/since.js';

test('a feature no stable release has yet is unreleased', () => {
  assert.equal(sinceLabel('0.5.0', '0.4.0'), 'unreleased');
  assert.equal(sinceLabel('0.10.0', '0.9.3'), 'unreleased');
  assert.equal(sinceLabel('1.0.0', '0.12.0'), 'unreleased');
});

test('a feature a stable release has says which', () => {
  assert.equal(sinceLabel('0.5.0', '0.5.0'), 'since 0.5.0');
  assert.equal(sinceLabel('0.5.0', '0.10.0'), 'since 0.5.0');
});

test('a since that is not a version fails the build', () => {
  assert.throws(() => sinceLabel('0.5', '0.4.0'), /not a version/);
});
