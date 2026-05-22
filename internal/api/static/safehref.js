'use strict';

// safeRedirectPath sanitizes a post-login redirect target (the `next` query
// param) so it can only ever point at a same-origin relative path. This blocks
// open-redirect abuse where /login.html?next=https://evil.com would bounce an
// authenticated victim off-site.
//
// `raw` is the untrusted value; `origin` is location.origin. The return value
// is always safe to assign to location.href: either a same-origin
// path+query+hash, or '/' when the input is empty, unparseable, or resolves to
// a different origin (absolute URL, protocol-relative //host, javascript:, or
// backslash tricks the URL parser normalizes to a host change).
function safeRedirectPath(raw, origin) {
  if (!raw) {
    return '/';
  }
  var resolved;
  try {
    resolved = new URL(raw, origin);
  } catch (e) {
    return '/';
  }
  if (resolved.origin !== origin) {
    return '/';
  }
  return resolved.pathname + resolved.search + resolved.hash;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { safeRedirectPath: safeRedirectPath };
}
