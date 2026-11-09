import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import App from "../App";
import { apiError, listItem, mockFetch } from "../test-fixtures";

const projects = { items: [{ id: "p-1", slug: "uppsel-production", name: "Uppsel Production", created_at: "2026-11-08T00:00:00Z" }] };

const components = {
  items: [
    { id: "c-1", slug: "payment-service", display_name: "Payment Service", environment: "production" },
    { id: "c-2", slug: "checkout-api", display_name: "Checkout API", environment: "production" },
    { id: "c-3", slug: "checkout-api", display_name: "Checkout API", environment: "staging" },
  ],
};

const page = (items = [listItem()], over: object = {}) => ({ items, next_cursor: null, open_count: 7, ...over });

const incidentCalls = (fetch: ReturnType<typeof mockFetch>) =>
  fetch.mock.calls.map(([u]) => new URL(String(u), "http://localhost")).filter((u) => u.pathname === "/v1/projects/p-1/incidents");

beforeEach(() => {
  sessionStorage.setItem("sentinel.token", "sk_usr_test");
  window.location.hash = "";
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
        open_count: 1,
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
      "GET /v1/projects/p-1/incidents": () => page([]),
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
          ? page([listItem({ id: "6f1c2a90-3333-4c4c-8888-000000000003", title: "Second page" })])
          : page([listItem()], { next_cursor: "abc" }),
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

  it("shows the number, analysis, signals, recorded cause and project open count", async () => {
    mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": () =>
        page([
          listItem({ number: 12, analysis_status: "running", signal_count: 3 }),
          listItem({ id: "6f1c2a90-2222-4c4c-8888-000000000002", number: 11, status: "resolved", analysis_status: "failed", resolution_note: "Pool exhausted after deploy" }),
        ]),
    });
    render(<App />);
    const open = await screen.findByRole("rowgroup", { name: "Open" });
    expect(within(open).getByText("INC-12")).toBeInTheDocument();
    expect(within(open).getByText("Running")).toBeInTheDocument();
    expect(within(open).getByText("grouped")).toBeInTheDocument();
    const resolved = screen.getByRole("rowgroup", { name: "Resolved" });
    expect(within(resolved).getByText("INC-11")).toBeInTheDocument();
    expect(within(resolved).getByText("Failed")).toBeInTheDocument();
    expect(within(resolved).getByText(/Cause \(recorded\): Pool exhausted after deploy/)).toBeInTheDocument();
    // open_count is project-wide, not the rows on screen.
    expect(screen.getByRole("link", { name: /Incidents/ })).toHaveTextContent("7");
    expect(screen.getByText("2 shown · 7 open")).toBeInTheDocument();
  });

  it("writes filters to the hash and sends them as parameters", async () => {
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/components": () => components,
      "GET /v1/projects/p-1/incidents": () => page([]),
    });
    render(<App />);
    await screen.findByText("No incidents match these filters.");

    // Component options are the project's distinct slugs.
    const comp = screen.getByRole("combobox", { name: "Component" });
    await waitFor(() => expect(within(comp).getAllByRole("option").map((o) => o.textContent)).toEqual(["All", "checkout-api", "payment-service"]));

    fireEvent.change(screen.getByRole("combobox", { name: "Severity" }), { target: { value: "critical" } });
    await waitFor(() => expect(window.location.hash).toBe("#/?severity=critical"));
    await waitFor(() => expect(incidentCalls(fetch).some((u) => u.searchParams.get("severity") === "critical")).toBe(true));

    fireEvent.change(screen.getByRole("combobox", { name: "Component" }), { target: { value: "checkout-api" } });
    await waitFor(() => expect(window.location.hash).toBe("#/?severity=critical&component=checkout-api"));

    const before = Date.now();
    fireEvent.change(screen.getByRole("combobox", { name: "Range" }), { target: { value: "7d" } });
    await waitFor(() => expect(window.location.hash).toBe("#/?severity=critical&component=checkout-api&range=7d"));
    await waitFor(() => expect(incidentCalls(fetch).some((u) => u.searchParams.has("since"))).toBe(true));
    const last = incidentCalls(fetch).at(-1)!;
    expect(last.searchParams.get("severity")).toBe("critical");
    expect(last.searchParams.get("component")).toBe("checkout-api");
    const since = Date.parse(last.searchParams.get("since")!);
    expect(Math.abs(before - 7 * 86_400_000 - since)).toBeLessThan(5_000);

    await userEvent.click(screen.getByRole("link", { name: "Clear filters" }));
    await waitFor(() => expect(window.location.hash).toBe("#/"));
    await waitFor(() => {
      const u = incidentCalls(fetch).at(-1)!;
      expect(["severity", "component", "since", "status"].some((k) => u.searchParams.has(k))).toBe(false);
    });
  });

  it("applies filters from the URL on load", async () => {
    window.location.hash = "#/?status=open&severity=error&component=checkout-api&range=24h&bogus=1";
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/components": () => components,
      "GET /v1/projects/p-1/incidents": () => page([]),
    });
    render(<App />);
    await screen.findByText("No incidents match these filters.");
    const first = incidentCalls(fetch)[0];
    expect(first.searchParams.get("status")).toBe("open");
    expect(first.searchParams.get("severity")).toBe("error");
    expect(first.searchParams.get("component")).toBe("checkout-api");
    expect(first.searchParams.has("since")).toBe(true);
    expect(screen.getByRole("combobox", { name: "Severity" })).toHaveValue("error");
    expect(screen.getByRole("button", { name: "Open" })).toHaveAttribute("aria-pressed", "true");
  });
});

describe("search", () => {
  it("opens on Ctrl-K, queries by title and opens the chosen incident", async () => {
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": (url) =>
        url.searchParams.get("q") === "pay"
          ? page([listItem({ number: 3, title: "Payment errors affecting checkout" }), listItem({ id: "6f1c2a90-4444-4c4c-8888-000000000004", number: 4, title: "Payments slow" })])
          : page([]),
    });
    render(<App />);
    await screen.findByText("No incidents match these filters.");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    const dialog = await screen.findByRole("dialog", { name: "Search incidents" });
    await userEvent.type(within(dialog).getByRole("combobox"), "pay");
    expect(await within(dialog).findByText("INC-4")).toBeInTheDocument();
    expect(incidentCalls(fetch).filter((u) => u.searchParams.has("q")).map((u) => u.searchParams.get("q"))).toEqual(["pay"]);

    await userEvent.keyboard("{ArrowDown}{Enter}");
    expect(window.location.hash).toBe("#/incidents/6f1c2a90-4444-4c4c-8888-000000000004");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("opens from the sidebar and closes on Escape", async () => {
    mockFetch({ "GET /v1/projects": () => projects, "GET /v1/projects/p-1/incidents": () => page([]) });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: /Search/ }));
    const dialog = await screen.findByRole("dialog", { name: "Search incidents" });
    await userEvent.type(within(dialog).getByRole("combobox"), "zzz");
    expect(await within(dialog).findByText("No incidents match “zzz”.")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("alerts page", () => {
  it("lists signals with links to their incidents", async () => {
    window.location.hash = "#/alerts";
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": () => page([]),
      "GET /v1/projects/p-1/signals": (url) =>
        url.searchParams.get("cursor")
          ? { items: [], next_cursor: null }
          : {
              items: [
                { event_id: "e-2", type: "alert.resolved", occurred_at: "2026-11-09T14:30:00Z", received_at: "2026-11-09T14:30:00Z", summary: "", severity: "info", component: { slug: "payment-service", display_name: "Payment Service" }, environment: "production", disposition: "unmatched_resolution", incident: null, source_url: null },
                { event_id: "e-1", type: "alert.triggered", occurred_at: "2026-11-09T14:09:12Z", received_at: "2026-11-09T14:09:12Z", summary: "Payment errors affecting checkout", severity: "critical", component: { slug: "payment-service", display_name: "Payment Service" }, environment: "production", disposition: "incident_created", incident: { id: "inc-9", number: 9 }, source_url: "javascript:alert(1)" },
              ],
              next_cursor: "next",
            },
    });
    render(<App />);
    const table = await screen.findByRole("table", { name: "Alerts" });
    await within(table).findByText("Payment errors affecting checkout");
    expect(within(table).getByRole("link", { name: "INC-9" })).toHaveAttribute("href", "#/incidents/inc-9");
    expect(within(table).getByText("No open incident")).toBeInTheDocument();
    expect(within(table).getByText("Opened")).toBeInTheDocument();
    // An unsafe source URL is not rendered as a link.
    expect(within(table).queryByRole("link", { name: "Payment errors affecting checkout" })).toBeNull();
    expect(screen.getByRole("link", { name: "Alerts" })).toHaveAttribute("aria-current", "page");
    // The sidebar still gets the project's open count.
    await waitFor(() => expect(screen.getByRole("link", { name: /Incidents/ })).toHaveTextContent("7"));

    await userEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(fetch.mock.calls.some(([u]) => String(u).includes("/signals?") && String(u).includes("cursor=next"))).toBe(true));
  });
});
