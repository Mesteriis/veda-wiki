// Exchange a reader key for the existing scoped session before loading state.
export async function loginFromLink(location, history, fetcher) {
  const url = new URL(location.href);
  const keys = url.searchParams.getAll("key");
  if (!keys.length) return false;
  url.searchParams.delete("key");
  try {
    history.replaceState(history.state, "", url.pathname + url.search + url.hash);
    if (keys.length !== 1 || !keys[0]) return true;
    const response = await fetcher("/api/session/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: keys[0] }),
    });
    return !response.ok;
  } catch {
    return true;
  }
}
