'use strict';
// Build the live-HLS playlist URL for a camera at a given quality tier.
//
// This is the single source of truth shared by two callers that MUST agree:
//   1. prewarmLiveHLS() - fired the instant the camera page knows it will
//      show live video, to start the server muxing before playback attaches.
//   2. startNativeHLS()'s warmup poll - the player's real first request.
// The server keys one HLS consumer per RTSP URL, and maps both "" and
// "low" quality to the detect substream. The high tier therefore sends NO
// query (server "" -> substream); the low tier sends ?quality=low (server
// "low" -> same substream). Either tier converges on the one consumer the
// pre-warm already started, so the head start is not wasted.
function liveHlsUrl(name, tier) {
  var base = '/api/cameras/' + encodeURIComponent(name) + '/live.m3u8';
  return tier === 'low' ? base + '?quality=low' : base;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { liveHlsUrl: liveHlsUrl };
}
