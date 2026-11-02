export function syncWarning(issue) {
  const messages = {
    error: "ошибка синхронизации GitHub",
    paused: "синхронизация GitHub на паузе",
    attention: "синхронизация GitHub требует внимания",
    checking: "идёт проверка GitHub",
    unverified: "состояние GitHub не подтверждено",
  };
  return Object.hasOwn(messages, issue) ? messages[issue] : "";
}
