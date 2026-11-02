// A 304 is usable only when this polling cycle already holds a matching state.
export async function fetchState(fetcher, url, signal, etag) {
  const response = await fetcher(url, { signal, headers: etag ? { 'If-None-Match': etag } : {} });
  if (response.status === 304 && etag) return { unchanged: true, etag };
  if (!response.ok) {
    const error = Error(response.status === 404 ? 'Страница не найдена' : 'Wiki временно недоступна');
    error.status = response.status;
    throw error;
  }
  return { data: await response.json(), etag: response.headers.get('ETag') || '' };
}
