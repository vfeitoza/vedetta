'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const { liveHlsUrl } = require('./livehls.js');

// The page pre-warms the live stream by fetching this exact URL, and the
// player's first real request must resolve to the SAME URL so the server
// reuses the one consumer it already started muxing (HLSManager keys
// consumers by RTSP URL). If these diverged, the pre-warm would warm a
// different consumer than the one playback consumes - useless.

test('high tier is the no-query playlist (server maps "" to the substream)', () => {
  assert.equal(liveHlsUrl('garage', 'high'), '/api/cameras/garage/live.m3u8');
});

test('low tier appends ?quality=low', () => {
  assert.equal(
    liveHlsUrl('garage', 'low'),
    '/api/cameras/garage/live.m3u8?quality=low',
  );
});

test('an unknown/absent tier behaves like high (no query)', () => {
  assert.equal(liveHlsUrl('garage'), '/api/cameras/garage/live.m3u8');
  assert.equal(liveHlsUrl('garage', 'medium'), '/api/cameras/garage/live.m3u8');
});

test('camera name is URL-encoded', () => {
  assert.equal(
    liveHlsUrl('back yard/1', 'high'),
    '/api/cameras/back%20yard%2F1/live.m3u8',
  );
});
