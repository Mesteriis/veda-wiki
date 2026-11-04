// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { field, fields } from "./forms";

describe("form fields", () => {
  const data = new FormData();
  data.append("id", "agent-one");
  data.append("sources", "wiki");
  data.append("sources", "infra");
  data.append("upload", new Blob(["x"]));

  it("reads text and treats missing or file fields as empty", () => {
    expect(field(data, "id")).toBe("agent-one");
    expect(field(data, "missing")).toBe("");
    expect(field(data, "upload")).toBe("");
  });

  it("collects every value of a repeated field", () => {
    expect(fields(data, "sources")).toEqual(["wiki", "infra"]);
    expect(fields(data, "upload")).toEqual([]);
  });
});
