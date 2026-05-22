'use strict';
// Run: node --test internal/api/static/safehref.test.js
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { safeRedirectPath } = require('./safehref.js');

const ORIGIN = 'https://vedetta.am8.nl';

// safeRedirectPath gates the post-login redirect target read from the `next`
// query param. It must only ever return a same-origin relative path so an
// attacker cannot craft /login.html?next=https://evil.com and bounce a victim
// off-site (open redirect) after they authenticate.

test('plain relative path is preserved', () => {
  assert.equal(safeRedirectPath('/cameras', ORIGIN), '/cameras');
});

test('relative path keeps query and hash', () => {
  assert.equal(safeRedirectPath('/event.html?id=42#clip', ORIGIN), '/event.html?id=42#clip');
});

test('empty or missing falls back to root', () => {
  assert.equal(safeRedirectPath('', ORIGIN), '/');
  assert.equal(safeRedirectPath(null, ORIGIN), '/');
  assert.equal(safeRedirectPath(undefined, ORIGIN), '/');
});

test('absolute off-site URL is rejected', () => {
  assert.equal(safeRedirectPath('https://evil.com/phish', ORIGIN), '/');
});

test('protocol-relative URL is rejected', () => {
  assert.equal(safeRedirectPath('//evil.com', ORIGIN), '/');
});

test('backslash protocol-relative trick is rejected', () => {
  assert.equal(safeRedirectPath('/\\/evil.com', ORIGIN), '/');
  assert.equal(safeRedirectPath('\\\\evil.com', ORIGIN), '/');
});

test('javascript scheme is rejected', () => {
  assert.equal(safeRedirectPath('javascript:alert(1)', ORIGIN), '/');
});

test('leading whitespace that resolves off-site is rejected', () => {
  assert.equal(safeRedirectPath('  //evil.com', ORIGIN), '/');
});

test('same-origin absolute URL collapses to its path', () => {
  assert.equal(safeRedirectPath(ORIGIN + '/settings', ORIGIN), '/settings');
});

// Dot-segment normalization can turn a same-origin input into a pathname that
// begins with // (a network-path reference). Returning that would still
// redirect off-site when assigned to location.href, so it must be rejected.
test('dot-segment normalization to a protocol-relative path is rejected', () => {
  assert.equal(safeRedirectPath('/.//evil.com', ORIGIN), '/');
  assert.equal(safeRedirectPath('/foo/..//evil.com', ORIGIN), '/');
  assert.equal(safeRedirectPath('/%2e%2e//evil.com', ORIGIN), '/');
  assert.equal(safeRedirectPath('/%2e//evil.com', ORIGIN), '/');
});

test('garbage that cannot be parsed falls back to root', () => {
  // A bare value with no scheme resolves against origin and stays same-origin,
  // so this asserts the relative-resolution branch does not throw.
  assert.equal(safeRedirectPath('cameras', ORIGIN), '/cameras');
});
