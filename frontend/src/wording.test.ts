// Guards the UI's honesty and safety rules across every source file.
import { expect, it } from "vitest";

const banned = ["root" + " cause", "dangerously" + "SetInnerHTML"];

// Every source file under src/, as raw text.
const sources = import.meta.glob<string>("./**/*.{ts,tsx,css,html}", { query: "?raw", import: "default", eager: true });

it("no source file uses banned wording or raw HTML injection", () => {
  const paths = Object.keys(sources);
  expect(paths.length).toBeGreaterThan(10);
  for (const path of paths) {
    const text = sources[path].toLowerCase();
    for (const word of banned) {
      expect(text.includes(word.toLowerCase()), `${path} contains "${word}"`).toBe(false);
    }
  }
});
