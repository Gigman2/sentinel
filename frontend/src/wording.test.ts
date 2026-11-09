// Guards the UI's honesty and safety rules across every source file.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

const banned = ["root" + " cause", "dangerously" + "SetInnerHTML"];

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? files(path) : [path];
  });
}

it("no source file uses banned wording or raw HTML injection", () => {
  const dir = dirname(fileURLToPath(import.meta.url));
  const all = files(dir);
  expect(all.length).toBeGreaterThan(10);
  for (const path of all) {
    const text = readFileSync(path, "utf8").toLowerCase();
    for (const word of banned) {
      expect(text.includes(word.toLowerCase()), `${path} contains "${word}"`).toBe(false);
    }
  }
});
