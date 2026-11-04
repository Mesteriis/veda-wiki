import { test } from "vitest";
import assert from "node:assert/strict";
import { githubErrorMessage, rateLimitedUntil } from "./githubErrors";

test("explains known errors without displaying arbitrary remote content", () => {
  assert.match(githubErrorMessage("invalid_docs_directory", 0, "fallback"), /docs должен быть каталогом/);
  assert.match(githubErrorMessage("invalid_veda_metadata", 0, "fallback"), /veda\.yaml/);
  assert.match(githubErrorMessage("veda_image_unavailable", 0, "fallback"), /Картинка из veda\.yaml/);
  assert.match(githubErrorMessage("local_import_active", 409, "fallback"), /локальный импорт/);
  assert.match(githubErrorMessage("rate_limited", 503, "fallback"), /лимит/);
  assert.match(githubErrorMessage("repository_type_changed", 0, "fallback"), /Верните прежнюю структуру/);
  assert.match(githubErrorMessage("asset_unavailable", 0, "fallback"), /резервную копию/);
  assert.match(githubErrorMessage("credential_like_content", 0, "fallback"), /пароль или ключ/);
  assert.match(githubErrorMessage("publication_confirmation_required", 0, "fallback"), /подтверждение публикации/);
  assert.match(githubErrorMessage("", 409, "fallback"), /настройки/i);
  assert.equal(githubErrorMessage("<private upstream response>", 503, "fallback"), "fallback");
  assert.equal(githubErrorMessage("constructor", 503, "fallback"), "fallback");
});

test("reports repository visibility only while its verification is current", async () => {
  const { githubVisibilityLabel } = await import("./githubErrors.js");
  const now = Date.parse("2026-01-01T12:00:00Z");
  const verified = { verification: "verified", verified_at: new Date(now).toISOString(), private: false, paused: false };
  assert.equal(githubVisibilityLabel(verified, now), "публичный");
  assert.equal(githubVisibilityLabel({ ...verified, private: true }, now), "приватный");
  assert.equal(githubVisibilityLabel({ ...verified, private: undefined }, now), "видимость не подтверждена");
  assert.equal(githubVisibilityLabel({ ...verified, paused: true }, now), "видимость не подтверждена");
  assert.equal(githubVisibilityLabel({ ...verified, verification: "failed" }, now), "видимость не подтверждена");
  assert.equal(githubVisibilityLabel({ ...verified, verified_at: "" }, now), "видимость не подтверждена");
  assert.equal(githubVisibilityLabel({ ...verified, verified_at: new Date(now - 599_999).toISOString() }, now), "публичный");
  assert.equal(githubVisibilityLabel({ ...verified, verified_at: new Date(now - 600_000).toISOString() }, now), "видимость не подтверждена");
  assert.equal(githubVisibilityLabel({ ...verified, verified_at: new Date(now + 60_001).toISOString() }, now), "видимость не подтверждена");
});

test("holds synchronization only while a rate limit has not reset", () => {
  const now = Date.parse("2026-11-04T10:00:00Z");
  assert.equal(rateLimitedUntil({ last_error: "rate_limited", next_attempt: "2026-11-04T10:05:00Z" }, now)?.toISOString(), "2026-11-04T10:05:00.000Z");
  assert.equal(rateLimitedUntil({ last_error: "rate_limited", next_attempt: "2026-11-04T09:55:00Z" }, now), null);
  assert.equal(rateLimitedUntil({ last_error: "rate_limited" }, now), null);
  assert.equal(rateLimitedUntil({ last_error: "rate_limited", next_attempt: "not a date" }, now), null);
  assert.equal(rateLimitedUntil({ last_error: "upstream_unavailable", next_attempt: "2026-11-04T10:05:00Z" }, now), null);
});
