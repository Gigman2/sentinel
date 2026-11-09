import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiFetch } from "./api";

afterEach(() => vi.unstubAllGlobals());

function stub(res: Response) {
  vi.stubGlobal("fetch", vi.fn(async () => res));
}

describe("apiFetch errors", () => {
  it("uses the API's JSON error and request ID", async () => {
    stub(new Response(JSON.stringify({ error: { code: "internal", message: "Internal error." }, request_id: "r-1" }), { status: 500 }));
    await expect(apiFetch("GET", "/v1/projects")).rejects.toMatchObject({ status: 500, code: "internal", message: "Internal error.", requestId: "r-1" });
  });

  it("explains a 5xx without a JSON body as the API being unreachable (the dev proxy's error)", async () => {
    stub(new Response("", { status: 500 }));
    const err = (await apiFetch("GET", "/v1/projects").catch((e: unknown) => e)) as ApiError;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("api_unreachable");
    expect(err.message).toMatch(/Couldn't reach the Sentinel API/);
  });

  it("keeps a generic message for other non-JSON errors", async () => {
    stub(new Response("nope", { status: 404 }));
    await expect(apiFetch("GET", "/v1/x")).rejects.toMatchObject({ status: 404, code: "http_error" });
  });
});
