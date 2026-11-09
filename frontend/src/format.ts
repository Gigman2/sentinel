// Formatting helpers. Every time is shown in UTC; the full ISO timestamp goes
// in the title attribute.

const pad = (n: number) => String(n).padStart(2, "0");
const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export function utcTime(iso: string | null, opts: { seconds?: boolean } = {}): { text: string; title: string } {
  if (!iso) return { text: "–", title: "" };
  const d = new Date(iso);
  const hm = `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
  return {
    text: opts.seconds ? `${hm}:${pad(d.getUTCSeconds())} UTC` : hm,
    title: d.toISOString(),
  };
}

/** "14:09 UTC" on the same UTC day as now, otherwise "8 Nov 22:14". */
export function whenSeen(iso: string, now: Date = new Date()): string {
  const d = new Date(iso);
  const hm = `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
  const sameDay = d.getUTCFullYear() === now.getUTCFullYear() && d.getUTCMonth() === now.getUTCMonth() && d.getUTCDate() === now.getUTCDate();
  return sameDay ? `${hm} UTC` : `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()].slice(0, 3)} ${hm}`;
}

export function relative(iso: string, now: Date = new Date()): string {
  const s = Math.round((now.getTime() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

export function gapLabel(prevIso: string, iso: string): string {
  const s = Math.round((new Date(iso).getTime() - new Date(prevIso).getTime()) / 1000);
  if (s <= 0) return "same time";
  if (s < 60) return `${s} s later`;
  if (s < 3600) return `${Math.round(s / 60)} min later`;
  if (s < 86400) return `${Math.round(s / 3600)} h later`;
  return `${Math.round(s / 86400)} d later`;
}

/** "Monday 9 November 2026", for the UTC date. */
export function utcDay(iso: string): string {
  const d = new Date(iso);
  return `${DAYS[d.getUTCDay()]} ${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

export function utcDateKey(iso: string): string {
  return new Date(iso).toISOString().slice(0, 10);
}

/** Only http and https URLs are rendered as links. */
export function safeHref(url: string | null | undefined): string | null {
  if (!url) return null;
  try {
    const u = new URL(url);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null;
  }
}

/** INC-n once incidents are numbered; until then the UUID's first 8 characters. */
export function shortId(id: string, number?: number): string {
  return number ? `INC-${number}` : id.slice(0, 8);
}

export function capitalise(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}
