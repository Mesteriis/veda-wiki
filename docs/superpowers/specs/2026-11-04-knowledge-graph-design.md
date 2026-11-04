# Граф знаний в VEDA — проектное решение

Дата: 2026-11-04. Источник требований: техническое задание владельца
(сохранено ниже дословно по смыслу разделов 1–13; при расхождении действует
задание). Этот документ фиксирует решения исполнителя, принятые до реализации.

## Проверенные факты (2026-11-04)

- `gliner2` на PyPI: 2.1.0, Python ≥3.10, протестирован на 3.10–3.12.
  Установка с PyTorch: `gliner2[local]`.
- Модель `fastino/gliner2.5-multi-v1`: ревизия Hub `cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3`,
  Apache-2.0, mDeBERTa-v3-base, 287M параметров, ~594 МБ весов, окно `max_len=4096`
  (дальше `extract` обрезает), устройства CPU/CUDA/MPS через `map_location`.
- Загрузчик: `AutoExtractor.from_pretrained("fastino/gliner2.5-multi-v1")`; старый
  `GLiNER2.from_pretrained` этот checkpoint не грузит.
- Сущности: `extract_entities(text, labels, include_confidence=True, include_spans=True)` →
  `{"entities": {label: [{"text","start","end","confidence"}]}}`, offsets — символы
  Python, полуоткрытые.
- Отношения (независимое декодирование): `extract_relations` →
  `{"relation_extraction": {rel: [{"head": {...}, "tail": {...}}]}}`, оценки только у концов,
  типы концов не проверяются.
- JointIE: `JointIE.from_pretrained(...)`, схема `entities([...]).relation(name, head, tail, ...)`,
  `extract(text, schema, config=JointIEConfig(...))` → `.feasible`, `to_dict()` с
  `entities[{id,type,text,start,end,confidence}]` и `relations[{type,head,tail,confidence}]`
  (head/tail — локальные id). `feasible=False` — неудовлетворённые ограничения.
- Long helpers (`extract_long`, `JointIE.extract_long`): спан/отношение сохраняется только если
  оба конца в одном чанке; поведение на выбранной версии проверяется до использования.
- Neo4j: образ `neo4j:2026.10.1-community` (календарные версии), Go driver
  `github.com/neo4j/neo4j-go-driver/v6` v6.4.0. Совместимость драйвера и сервера проверяется
  интеграционным smoke; при несовместимости — `v5` с образом `5.26.x-community` и записью причины.
- Среда разработки: macOS, Apple M5 Max, 64 ГБ, Docker Desktop 29.9, uv, pyenv с 3.11/3.12.

## Решения исполнителя

1. **Границы пакетов.** `internal/knowledge` — домен и сервис (контракты, подготовка текста,
   разрешение сущностей, политика доступа, сервис чтения, manifest/reconciliation, worker).
   Адаптеры: `internal/knowledge/extractorclient` (HTTP к Python),
   `internal/knowledge/neo4jindex` (Neo4j), `internal/knowledge/memindex` (детерминированный
   индекс в памяти для `make test` и как эталон поведения), `internal/knowledge/jobs`
   (служебная SQLite). `internal/httpapi` и `internal/mcp` — адаптеры чтения;
   `internal/app` — wiring, флаги, workers, shutdown. Пакеты регистрируются в
   `scripts/check_architecture.py` с явными направлениями импорта.
2. **Подготовка текста — в Go.** Go превращает Markdown видимой ревизии в блоки (заголовок,
   абзац, элемент списка, ячейки таблицы), исключая кодовые блоки, inline-код и frontmatter
   (политика: не извлекать), и строит source map от координат блока к исходному Markdown.
   Единица координат везде — Unicode code point, диапазоны полуоткрытые. Python получает
   блоки, режет их по реальному token budget (токенизатор модели плюс схема) с перекрытием и
   возвращает спаны в координатах блока; Go проверяет границы и совпадение цитаты.
3. **Путь извлечения.** Основной — JointIE (типы концов задаются схемой). Если на
   выбранной версии JointIE не годится (проверяется в задаче extractor на реальной модели),
   используется `extract` со схемой entities+relations и проверка типов концов в Go;
   выбор и причина фиксируются в `docs/knowledge-graph.md`. `feasible=False` — статус
   `infeasible` у чанка, не пустой граф.
4. **Схема.** Сущности: person, organization, project, service, host, repository, technology,
   document. Отношения с допустимыми концами: `depends_on` (service|project → service|technology|repository),
   `runs_on` (service → host), `uses` (project|service|person → technology|service),
   `part_of` (service|project|host|repository → project|organization),
   `maintained_by` (project|service|repository → person|organization),
   `documents` (document → project|service|host|repository|technology).
   Схема версионируется и входит в pipeline fingerprint.
5. **Статусы утверждений.** Модель даёт гипотезу. Go помечает утверждение `extracted`;
   если в том же предложении есть маркеры отрицания, плана, примера или прошлого
   (фиксированный версионированный список для ru/en), статус `uncertain` с указанием
   маркера. Иной классификации не делается.
6. **Entity resolution.** Ключ канонической сущности: тип + нормализованное имя по правилам
   типа (host/repository/service — технический ключ в нижнем регистре без пробельных
   вариаций; person/organization — только внутри одного источника, между источниками —
   кандидат). Смешение письменностей не нормализуется. Явные алиасы — файл
   `<data>/knowledge-aliases.json` (пользовательская конфигурация, не удаляется с
   индексом). Решение о совпадении хранит основание; кандидаты отдаются как кандидаты.
7. **Хранение.** `<data>/knowledge.sqlite` (modernc), 0600, свои миграции: manifest снимков,
   задачи, результаты извлечения по (source_id, path, content_sha256, fingerprint) —
   неизменяемые. Neo4j — производный индекс по снимку+fingerprint: публикация снимка одной
   транзакцией, чтение только по разрешённому набору снимков.
8. **Активные снимки.** Для каждой статьи до двух: текущая (последняя ревизия) и
   опубликованная (`PublicRevision`), если SHA различаются.
9. **Доступ.** На каждом запросе Go строит разрешённый набор `(source_id, path, sha)` из
   request-scoped snapshot по правилам `ReadSnapshot`/`ReadPage` с вычисленным
   `public := !owner && !reader`, передаёт его в индекс параметром; индекс фильтрует
   evidence до обхода и агрегирования; имена, алиасы, счётчики, ранжирование строятся только
   из разрешённых оснований.
10. **Сигналы.** `Tx.AfterCommit` — неблокирующая отправка в канал ёмкостью 1; основная
    гарантия — периодическая и стартовая сверка manifest со store.
11. **Python-сервис.** Python 3.12, `uv` lock, Starlette+uvicorn, pydantic-схемы v1 запроса и
    ответа, один процесс, семафор inference (по умолчанию 1), лимиты размера/таймауты,
    служебный ключ в заголовке, только loopback/compose-сеть. Тесты с фейковой моделью;
    реальная модель — отдельная команда.
12. **Конфигурация.** Флаги `--knowledge` (выключено по умолчанию), `--knowledge-extractor-url`,
    `--knowledge-extractor-key-file`, `--knowledge-neo4j-uri`, `--knowledge-neo4j-user`,
    `--knowledge-neo4j-password-file`, `--knowledge-workers`, `--knowledge-reconcile-interval`;
    схема и лимиты — в версионируемом файле в репозитории.
13. **UI.** Без новой библиотеки графа: существующий SVG-граф расширяется режимом
    «Граф знаний» с ограниченным подграфом и текстовым списком связей.
14. **Ограничения проверки.** Docker CPU-путь не выдаётся за проверку MPS; MPS
    проверяется нативным запуском на этом Mac и документируется отдельно.

## Требования

Полный текст технического задания владельца — в сообщении от 2026-11-04 (разделы 1–13:
чтение проекта; пользовательский результат; архитектурные инварианты; компоненты и
хранение; извлекатель GLiNER2.5; entity resolution; фоновая индексация; авторизация на
уровне evidence; REST/MCP; интерфейс; конфигурация; проверки 1–13; документация).
Исполнители получают его дословно в брифах задач (`docs/superpowers/specs/2026-11-04-knowledge-graph-brief.md`).
