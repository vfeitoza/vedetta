'use strict';
// Run: node --test internal/api/static/timeparse.test.js
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { resolvePlaybackTime } = require('./timeparse.js');

// resolvePlaybackTime interprets the camera.html ?t= parameter. It returns a
// Date ONLY for a deliberate recording deep-link (an ISO 8601 timestamp, which
// is the exact and only shape every Vedetta link/notification generator emits).
// For anything else - absent, empty, garbage, or a bare epoch-digit value
// accidentally frozen into a home-screen launch URL - it returns null, meaning
// "no playback target: start the live view". A present-but-unhandled t must
// never strand the page on the snapshot loop.

test('absent t yields null (start live)', () => {
  assert.equal(resolvePlaybackTime(null), null);
  assert.equal(resolvePlaybackTime(undefined), null);
});

test('empty or whitespace t yields null (start live)', () => {
  assert.equal(resolvePlaybackTime(''), null);
  assert.equal(resolvePlaybackTime('   '), null);
});

test('frozen epoch-seconds launch-URL cruft yields null (start live)', () => {
  // The exact value frozen into the user's home-screen PWA icon.
  assert.equal(resolvePlaybackTime('1778947639'), null);
});

test('epoch-milliseconds digits yield null (start live)', () => {
  assert.equal(resolvePlaybackTime('1778947639000'), null);
});

test('unparseable garbage yields null (start live)', () => {
  assert.equal(resolvePlaybackTime('not-a-date'), null);
  assert.equal(resolvePlaybackTime('garage'), null);
});

test('ISO 8601 UTC deep-link yields the corresponding Date (playback)', () => {
  const d = resolvePlaybackTime('2026-05-16T13:27:19Z');
  assert.ok(d instanceof Date);
  assert.equal(d.getTime(), Date.parse('2026-05-16T13:27:19Z'));
});

test('ISO 8601 with timezone offset (link generator format) yields a Date', () => {
  const d = resolvePlaybackTime('2026-05-16T15:27:19+02:00');
  assert.ok(d instanceof Date);
  assert.equal(d.getTime(), Date.parse('2026-05-16T15:27:19+02:00'));
});
