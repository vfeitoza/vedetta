'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const { iceServersFromResponse } = require('./iceservers.js');

// The browser is the WebRTC offerer, so it must build its own
// RTCConfiguration.iceServers from the server's config endpoint. The
// privacy-first default is an empty list: with no configured STUN/TURN, the
// browser offers only host candidates and leaks no IP to a third party. A
// missing, null, or malformed payload must therefore degrade to [] - never to
// a hardcoded public STUN server.

test('null or undefined payload yields no ICE servers', () => {
  assert.deepEqual(iceServersFromResponse(null), []);
  assert.deepEqual(iceServersFromResponse(undefined), []);
});

test('payload without ice_servers yields no ICE servers', () => {
  assert.deepEqual(iceServersFromResponse({}), []);
});

test('non-array ice_servers yields no ICE servers', () => {
  assert.deepEqual(iceServersFromResponse({ ice_servers: 'nope' }), []);
});

test('empty list passes through as empty', () => {
  assert.deepEqual(iceServersFromResponse({ ice_servers: [] }), []);
});

test('configured STUN and TURN servers pass through unchanged', () => {
  const payload = {
    ice_servers: [
      { urls: ['stun:stun.example.net:3478'] },
      { urls: ['turn:turn.example.net:3478'], username: 'u', credential: 'p' },
    ],
  };
  assert.deepEqual(iceServersFromResponse(payload), payload.ice_servers);
});
