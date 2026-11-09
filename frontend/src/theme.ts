// Light/dark theme: follows the system until the person picks one, which is
// remembered in this browser.

export type Theme = "light" | "dark";
const KEY = "sentinel.theme";

export function initialTheme(): Theme {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === "light" || saved === "dark") return saved;
  } catch {
    // Storage unavailable; fall through to the system setting.
  }
  return typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function applyTheme(theme: Theme, remember: boolean) {
  document.documentElement.dataset.theme = theme;
  if (!remember) return;
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    // Not remembered; it still applies to this page.
  }
}
