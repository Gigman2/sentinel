import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import App from "../App";
import { apiError, listItem, mockFetch } from "../test-fixtures";

const projects = { items: [{ id: "p-1", slug: "uppsel-production", name: "Uppsel Production", created_at: "2026-11-08T00:00:00Z" }] };

beforeEach(() => {
  sessionStorage.setItem("sentinel.token", "sk_usr_test");
});

describe("incident list", () => {
  it("groups rows under Open and Resolved", async () => {
    mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": () => ({
        items: [
          listItem(),
          listItem({ id: "6f1c2a90-2222-4c4c-8888-000000000002", status: "resolved", severity: "warning", title: "Latency above threshold" }),
        ],
        next_cursor: null,
      }),
    });
    render(<App />);
    const open = await screen.findByRole("rowgroup", { name: "Open" });
    expect(within(open).getByText("Payment errors affecting checkout")).toBeInTheDocument();
    const resolved = screen.getByRole("rowgroup", { name: "Resolved" });
    expect(within(resolved).getByText("Latency above threshold")).toBeInTheDocument();
    expect(screen.getByText("Uppsel Production")).toBeInTheDocument();
  });

  it("filters by status on the server", async () => {
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": () => ({ items: [], next_cursor: null }),
    });
    render(<App />);
    await screen.findByText("No incidents match these filters.");
    await userEvent.click(screen.getByRole("button", { name: "Resolved" }));
    await screen.findByText("No incidents match these filters.");
    const urls = fetch.mock.calls.map(([u]) => String(u));
    expect(urls.some((u) => u.includes("/incidents?") && u.includes("status=resolved"))).toBe(true);
  });

  it("loads more with the cursor", async () => {
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": (url) =>
        url.searchParams.get("cursor")
          ? { items: [listItem({ id: "6f1c2a90-3333-4c4c-8888-000000000003", title: "Second page" })], next_cursor: null }
          : { items: [listItem()], next_cursor: "abc" },
    });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Second page")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
    expect(fetch.mock.calls.some(([u]) => String(u).includes("cursor=abc"))).toBe(true);
  });

  it("shows sign-in when the token is rejected", async () => {
    mockFetch({ "GET /v1/projects": () => apiError(401, "unauthorized") });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Sign in to Sentinel" })).toBeInTheDocument();
    expect(sessionStorage.getItem("sentinel.token")).toBeNull();
  });
});
