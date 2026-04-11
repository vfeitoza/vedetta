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
  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
      for (const w of wins) {
        if (w.url.endsWith(url) && 'focus' in w) return w.focus();
      }
      return clients.openWindow(url);
    })
  );
});
