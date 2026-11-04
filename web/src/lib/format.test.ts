import { describe, expect, it } from "vitest";
import { pageHref, updatedLabel } from "./format";

describe("format", () => {
  it("encodes the article path into the page link", () => {
    expect(pageHref("docs/a b/c.md")).toBe("/?page=docs%2Fa%20b%2Fc.md");
  });

  it("labels a missing or invalid update time as unknown", () => {
    expect(updatedLabel(undefined)).toBe("Время обновления неизвестно");
    expect(updatedLabel("")).toBe("Время обновления неизвестно");
    expect(updatedLabel("not a date")).toBe("Время обновления неизвестно");
    expect(updatedLabel("2026-01-02T03:04:05Z")).toMatch(/^Обновлено /);
  });
});
