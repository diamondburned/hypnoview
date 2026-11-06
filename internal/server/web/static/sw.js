const CACHE_NAME = "__CACHE_VERSION__";
const PRECACHE_URLS = [
	"/static/shell.html",
	"/static/style.css",
	"/static/app.js",
	"/static/htmx.min.js",
	"/static/spiral.svg",
	"/static/icon-192.png",
	"/static/icon-512.png",
	"/static/icon-maskable-512.png",
	"/static/apple-touch-icon.png",
	"/manifest.webmanifest"
];

self.addEventListener("install", (event) => {
	event.waitUntil(
		caches.open(CACHE_NAME).then((cache) => {
			return cache.addAll(PRECACHE_URLS);
		}).then(() => {
			return self.skipWaiting();
		})
	);
});

self.addEventListener("activate", (event) => {
	event.waitUntil(
		caches.keys().then((keys) => {
			return Promise.all(
				keys.map((key) => {
					if (key !== CACHE_NAME) {
						return caches.delete(key);
					}
				})
			);
		}).then(() => {
			return self.clients.claim();
		})
	);
});

self.addEventListener("fetch", (event) => {
	const request = event.request;
	if (request.method !== "GET") return;

	const url = new URL(request.url);
	// Only handle same-origin requests
	if (url.origin !== self.location.origin) return;

	// Bypass service worker when the shell explicitly fetches SSR content directly
	if (request.headers.get("X-Hypnogaze-Shell") === "1") {
		return;
	}

	// Navigation requests: HTML pages
	if (request.mode === "navigate") {
		// Case A: Cold PWA launch (e.g. /?pwa=1) -> serve static shell instantly at 0ms
		if (url.searchParams.get("pwa") === "1") {
			event.respondWith(
				caches.match("/static/shell.html").then((cached) => {
					return cached || fetch(request);
				})
			);
			return;
		}

		// Case B: Regular SSR page navigation -> Network-First with fast timeout (3.5s), fallback to shell
		event.respondWith(
			(async () => {
				const controller = new AbortController();
				const timeoutId = setTimeout(() => controller.abort(), 3500);

				try {
					const response = await fetch(request, {
						signal: controller.signal,
						referrer: request.referrer,
						referrerPolicy: request.referrerPolicy,
					});
					clearTimeout(timeoutId);
					return response;
				} catch (err) {
					clearTimeout(timeoutId);
					const cachedShell = await caches.match("/static/shell.html");
					if (cachedShell) {
						return cachedShell;
					}
					throw err;
				}
			})()
		);
		return;
	}

	// Static assets & manifest
	if (url.pathname.startsWith("/static/") || url.pathname === "/manifest.webmanifest") {
		const isDev = url.hostname === "localhost" || url.hostname === "127.0.0.1";
		if (isDev) {
			// Dev mode: Network-first
			event.respondWith(
				fetch(request).catch(() => caches.match(request))
			);
			return;
		}

		// Production: Stale-while-revalidate / Cache-first
		event.respondWith(
			caches.match(request).then((cached) => {
				const fetchPromise = fetch(request).then((networkResponse) => {
					if (networkResponse && networkResponse.status === 200) {
						const clone = networkResponse.clone();
						caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
					}
					return networkResponse;
				}).catch(() => null);

				return cached || fetchPromise;
			})
		);
	}
});
