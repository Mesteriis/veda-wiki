import test from "node:test";
import assert from "node:assert/strict";
import { observationLabel, syncWarning } from "./sourceWarnings.js";

test("explains known sync states without displaying arbitrary input", () => {
  assert.match(syncWarning("error"), /ошибка синхронизации/);
  assert.match(syncWarning("paused"), /на паузе/);
  assert.match(syncWarning("unverified"), /не подтверждено/);
  assert.equal(syncWarning("<untrusted>"), "");
  assert.equal(syncWarning("constructor"), "");
});

test("shows a known import observation and handles unknown timestamps", () => {
  assert.match(observationLabel("2026-11-02T04:00:00Z"), /^последний импорт: /);
  for (const value of [undefined, "", "invalid", null]) {
    assert.equal(observationLabel(value), "время последнего импорта неизвестно");
  }
});
