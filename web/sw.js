'use strict';
// Service worker de Cofre: recibe lo que compartes desde otras apps (Galería, Archivos…)
// con el menú "Compartir" de Android, lo guarda un momento y abre Cofre para subirlo
// con tu sesión normal. No cachea nada más: Cofre siempre funciona en directo.

const BANDEJA = 'cofre-compartido';

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'POST' || url.pathname !== '/compartir') return;
  e.respondWith(recibir(e.request));
});

async function recibir(request) {
  try {
    const form = await request.formData();
    const cache = await caches.open(BANDEJA);
    const stamp = Date.now();
    const files = form.getAll('file').filter((f) => typeof f === 'object' && f.size > 0);
    await Promise.all(files.map((f, i) =>
      cache.put(`/compartido/${stamp}-${i}`, new Response(f, {
        headers: { 'Content-Type': f.type || 'application/octet-stream', 'X-Nombre': encodeURIComponent(f.name || 'archivo') },
      }))));
    // Muchas apps comparten enlaces o textos en lugar de archivos.
    const texto = [form.get('title'), form.get('text'), form.get('url')]
      .filter((t) => typeof t === 'string' && t.trim())
      .filter((t, i, all) => all.indexOf(t) === i)
      .join('\n');
    if (!files.length && texto) {
      await cache.put(`/compartido/${stamp}-texto`, new Response(texto, { headers: { 'Content-Type': 'text/plain', 'X-Texto': '1' } }));
    }
    return Response.redirect('/?compartido=1', 303);
  } catch (err) {
    return Response.redirect('/?compartido=error', 303);
  }
}
