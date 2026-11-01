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
