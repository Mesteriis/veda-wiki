// Only known server codes are mapped; arbitrary remote response text is never displayed.
export function githubErrorMessage(code, status, fallback) {
  const messages = {
    rate_limited: "GitHub ограничил число запросов. Дождитесь сброса лимита или выберите другой аккаунт.",
    repository_unavailable: "Репозиторий или ветка недоступны. Проверьте адрес и доступ выбранного аккаунта.",
    permission_denied: "GitHub отказал в доступе. Проверьте права выбранного аккаунта.",
    upstream_unavailable: "GitHub временно недоступен. Повторите позже.",
  };
  if (Object.hasOwn(messages, code)) return messages[code];
  if (status === 409) return "Настройки изменились или действует ограничение синхронизации. Обновите список и проверьте состояние.";
  return fallback;
}

// Estimate proof freshness using the browser clock; the server independently
// enforces its own clock and authorization for every public read.
export function githubVisibilityLabel(connection, now = Date.now()) {
  const verified = Date.parse(connection.verified_at);
  if (typeof connection.private !== 'boolean' || connection.paused || connection.verification !== 'verified' || !Number.isFinite(verified)
      || verified > now + 60_000 || now - verified >= 600_000) {
    return 'видимость не подтверждена';
  }
  return connection.private ? 'приватный' : 'публичный';
}
