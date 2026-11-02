import test from "node:test";
import assert from "node:assert/strict";
import { syncWarning } from "./sourceWarnings.js";

test("explains known sync states without displaying arbitrary input", () => {
  assert.match(syncWarning("error"), /ошибка синхронизации/);
  assert.match(syncWarning("paused"), /на паузе/);
  assert.match(syncWarning("unverified"), /не подтверждено/);
  assert.equal(syncWarning("<untrusted>"), "");
  assert.equal(syncWarning("constructor"), "");
});
