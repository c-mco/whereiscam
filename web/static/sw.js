// whereis.cam Service Worker for PWA and Web Push
const CACHE_NAME = 'whereiscam-v1';
const ASSETS_TO_CACHE = [
  '/',
  '/static/style.css',
  '/static/fonts/calistoga-latin.woff2',
  '/static/fonts/josefinsans-latin.woff2',
  '/static/beer-mug.svg',
  '/static/question.svg',
  '/static/radar-sweep.svg',
  '/static/arrow-up-right.svg',
  '/static/favicon.svg'
];

self.addEventListener('install', (event) => {
  self.skipWaiting();
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(ASSETS_TO_CACHE))
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) => {
      return Promise.all(
        keys.filter((key) => key !== CACHE_NAME).map((key) => caches.delete(key))
      );
    }).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  if (event.request.url.includes('/api/') || event.request.mode === 'navigate') {
    return;
  }
  event.respondWith(
    caches.match(event.request).then((cached) => {
      return cached || fetch(event.request);
    })
  );
});

// Push Notification Event Listener
self.addEventListener('push', (event) => {
  let data = {};
  if (event.data) {
    try {
      data = event.data.json();
    } catch (e) {
      data = { title: event.data.text() };
    }
  }

  const title = data.title || "★ Cam is at the pub!";
  const options = {
    body: data.body || "Pints are flowing. Pull up a stool and say g'day!",
    icon: data.icon || "/static/favicon.svg",
    badge: data.badge || "/static/favicon.svg",
    vibrate: [200, 100, 200],
    data: { url: data.url || "/" }
  };

  event.waitUntil(self.registration.showNotification(title, options));
});

// Notification Click Event Listener
self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const targetUrl = (event.notification.data && event.notification.data.url) || "/";

  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windowClients) => {
      for (let client of windowClients) {
        if (client.url === targetUrl && 'focus' in client) {
          return client.focus();
        }
      }
      if (clients.openWindow) {
        return clients.openWindow(targetUrl);
      }
    })
  );
});
