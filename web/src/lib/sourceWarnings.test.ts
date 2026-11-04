import { test } from "vitest";
import assert from "node:assert/strict";
import { conflictWarning, observationLabel, syncWarning } from "./sourceWarnings";

test("explains known sync states without displaying arbitrary input", () => {
  assert.match(syncWarning("error"), /ошибка синхронизации/);
  assert.match(syncWarning("paused"), /на паузе/);
  assert.match(syncWarning("unverified"), /не подтверждено/);
  assert.equal(syncWarning("<untrusted>"), "");
  assert.equal(syncWarning("constructor"), "");
});

test("explains when a pending write prevents conflict resolution", () => {
  assert.equal(conflictWarning({}), "");
  assert.match(conflictWarning({ unresolved_conflict: true }), /Проверьте его состояние/);
  const blocked = conflictWarning({ unresolved_conflict: true, conflict_resolution_blocked_by_write: true });
  assert.match(blocked, /отправка блокирует/);
  assert.doesNotMatch(blocked, /разрешите конфликт/);
});

test("shows a known import observation and handles unknown timestamps", () => {
  assert.match(observationLabel("2026-11-02T04:00:00Z"), /^последний импорт: /);
  for (const value of [undefined, "", "invalid", null]) {
    assert.equal(observationLabel(value), "время последнего импорта неизвестно");
  }
});
