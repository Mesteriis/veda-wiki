// Only known server codes are mapped; arbitrary remote response text is never displayed.
export function githubErrorMessage(code, status, fallback) {
  const messages = {
    rate_limited: "GitHub ограничил число запросов. Дождитесь сброса лимита или выберите другой аккаунт.",
    repository_unavailable: "Репозиторий или ветка недоступны. Проверьте адрес и доступ выбранного аккаунта.",
    permission_denied: "GitHub отказал в доступе. Проверьте права выбранного аккаунта.",
    upstream_unavailable: "GitHub временно недоступен. Повторите позже.",
    asset_unavailable: "Изображение отсутствует или повреждено, а текущий снимок не позволяет его восстановить. Проверьте источник и резервную копию.",
    invalid_docs_directory: "В репозитории docs должен быть каталогом, а не файлом, ссылкой или подмодулем. Локальные статьи сохранены.",
    invalid_veda_metadata: "Проверьте veda.yaml в корне репозитория: файл должен соответствовать схеме VEDA. Локальные статьи сохранены.",
    veda_image_unavailable: "Картинка из veda.yaml отсутствует или не поддерживается. Проверьте путь и формат; локальные статьи сохранены.",
    credential_like_content: "Отправка остановлена: текст похож на пароль или ключ. Проверьте данные и сохраните исправленную статью.",
    repository_type_changed: "Тип репозитория изменился. Верните прежнюю структуру и повторите синхронизацию; локальные статьи сохранены.",
    source_conflict: "Обнаружен конфликт с локальными изменениями. Проверьте конфликт перед продолжением синхронизации.",
    local_import_active: "Для этого источника настроен локальный импорт. Сначала перенастройте импорт; статьи останутся на месте.",
    repository_limit: "Документация репозитория превышает допустимый размер или количество файлов.",
    credentials_missing: "Сохранённые данные аккаунта недоступны. Проверьте подключение аккаунта GitHub.",
    publication_confirmation_required: "Для отправки ранее приватных изменений необходимо подтверждение публикации.",
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
