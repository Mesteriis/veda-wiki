import { test } from "vitest";
import assert from "node:assert/strict";
import { githubSourceId } from "./githubSourceId";
test("derive a valid source ID from an owner/repository or GitHub URL", () => {
  assert.equal(githubSourceId("Example/handbook"), "github-example-handbook");
  assert.equal(githubSourceId("https://github.com/Example/Repo.git/"), "github-example-repo");
  assert.equal(githubSourceId("not a repository"), "");
  assert.equal(githubSourceId("https://example.org/a/b"), "");
  assert.match(githubSourceId("x".repeat(80) + "/repo"), /^[a-z0-9][a-z0-9_-]{0,63}$/);
});
