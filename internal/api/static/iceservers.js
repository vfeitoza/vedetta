'use strict';
// Extract the RTCConfiguration.iceServers array from the /api/streaming/
// ice-servers response. The browser is the WebRTC offerer, so its ICE servers
// are not signaled by the server's answer - it must configure them itself.
//
// The privacy-first default is no external ICE: any missing, null, or
// malformed payload degrades to [] so a default install leaks no viewer IP to
// a third-party STUN operator. Each entry already matches the browser's
// RTCIceServer shape ({ urls, username, credential }), so it passes through
// unchanged.
function iceServersFromResponse(payload) {
  if (!payload || !Array.isArray(payload.ice_servers)) {
    return [];
  }
  return payload.ice_servers;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { iceServersFromResponse: iceServersFromResponse };
}
