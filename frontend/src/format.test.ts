import { describe, expect, it } from "vitest";
import { gapLabel, relative, safeHref, shortId, utcDay, utcTime, whenSeen } from "./format";

describe("safeHref", () => {
  it("accepts http and https", () => {
    expect(safeHref("https://deploys.example/payments/1.8.4")).toBe("https://deploys.example/payments/1.8.4");
    expect(safeHref("http://x.example/")).toBe("http://x.example/");
  });
  it("rejects other schemes and junk", () => {
    expect(safeHref("javascript:alert(1)")).toBeNull();
    expect(safeHref("data:text/html,hi")).toBeNull();
    expect(safeHref("not a url")).toBeNull();
    expect(safeHref(null)).toBeNull();
  });
});

describe("utcTime", () => {
  it("formats minutes and seconds in UTC", () => {
    expect(utcTime("2026-11-09T14:09:12+02:00")).toEqual({ text: "12:09", title: "2026-11-09T12:09:12.000Z" });
    expect(utcTime("2026-11-09T14:09:12Z", { seconds: true }).text).toBe("14:09:12 UTC");
  });
  it("renders a dash for null", () => {
    expect(utcTime(null)).toEqual({ text: "–", title: "" });
  });
});

describe("whenSeen", () => {
  const now = new Date("2026-11-09T16:00:00Z");
  it("uses the time alone on the same UTC day, the date otherwise", () => {
    expect(whenSeen("2026-11-09T14:09:00Z", now)).toBe("14:09 UTC");
    expect(whenSeen("2026-11-08T22:14:00Z", now)).toBe("8 Nov 22:14");
  });
});

describe("relative", () => {
  const now = new Date("2026-11-09T16:00:00Z");
  it("covers seconds to days", () => {
    expect(relative("2026-11-09T15:59:30Z", now)).toBe("just now");
    expect(relative("2026-11-09T15:58:00Z", now)).toBe("2 min ago");
    expect(relative("2026-11-09T13:00:00Z", now)).toBe("3 h ago");
    expect(relative("2026-11-06T16:00:00Z", now)).toBe("3 d ago");
  });
});

describe("gapLabel", () => {
  it("describes the time between events", () => {
    expect(gapLabel("2026-11-09T14:02:31Z", "2026-11-09T14:07:31Z")).toBe("5 min later");
    expect(gapLabel("2026-11-09T14:09:00Z", "2026-11-09T14:09:40Z")).toBe("40 s later");
    expect(gapLabel("2026-11-09T14:09:00Z", "2026-11-09T14:09:00Z")).toBe("same time");
    expect(gapLabel("2026-11-09T10:00:00Z", "2026-11-09T13:00:00Z")).toBe("3 h later");
  });
});

it("utcDay names the UTC day", () => {
  expect(utcDay("2026-11-09T23:30:00-02:00")).toBe("Tuesday 10 November 2026");
});

it("shortId shows the first 8 characters", () => {
  expect(shortId("6f1c2a90-1111-4c4c-8888-000000000001")).toBe("6f1c2a90");
  expect(shortId("6f1c2a90-1111-4c4c-8888-000000000001", 42)).toBe("INC-42");
});
