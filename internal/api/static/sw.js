// Vedetta service worker — push + notificationclick only.
// No offline caching, no fetch handler, no asset pre-cache.
// See docs/superpowers/specs/2026-04-11-installable-pwa-with-push-notifications-design.md

self.addEventListener('push', (event) => {
  // WebKit revokes the push subscription if this handler completes without
  // calling showNotification. Wrap everything and always show SOMETHING.
  event.waitUntil((async () => {
    let title = 'Vedetta';
    let options = {
      body: 'Detection event',
      icon: '/icon-192.png',
      badge: '/badge-72.png',
      tag: 'vedetta-generic',
    };
    try {
      const data = event.data ? event.data.json() : null;
      if (data && data.title) {
        title = data.title;
        options = {
          body: data.body || 'Detection event',
          icon: '/icon-192.png',
          badge: '/badge-72.png',
          tag: data.tag || 'vedetta-generic',
          data: { url: data.url || '/' },
          timestamp: data.ts ? data.ts * 1000 : Date.now(),
        };
        if (data.image) options.image = data.image;
      }
    } catch (err) {
      // Malformed payload — fall through to the generic notification above.
    }
    await self.registration.showNotification(title, options);
  })());
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/';
  event.waitUntil((async () => {
    const wins = await clients.matchAll({ type: 'window', includeUncontrolled: true });
    // If an existing window is already at the target URL, just focus it.
    for (const w of wins) {
      if (w.url.endsWith(url) && 'focus' in w) return w.focus();
    }
    // If ANY client is open, focus it and tell it to navigate. On iOS
    // inside a standalone PWA, clients.openWindow() focuses the existing
    // window without navigating — this postMessage is the workaround.
    if (wins.length > 0 && 'focus' in wins[0]) {
      await wins[0].focus();
      wins[0].postMessage({ type: 'notify-navigate', url: url });
      return;
    }
    // No clients — open a fresh window.
    return clients.openWindow(url);
  })());
});
