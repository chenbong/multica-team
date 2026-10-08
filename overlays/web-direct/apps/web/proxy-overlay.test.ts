// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";
import { proxy } from "./proxy";

afterEach(() => vi.unstubAllEnvs());
describe("deployment root routing", () => {
  it.each(["http://192.0.2.10:8004", "http://mesh.example.test:8004", "https://app.example.test"])(
    "serves login at the root for %s with or without a session cookie", (origin) => {
      for (const cookie of ["", "multica_logged_in=1; last_workspace_slug=example"]) {
        const response = proxy(new NextRequest(`${origin}/?next=%2Fexample%2Fissues`, {
          headers: { cookie, "accept-language": "zh-CN" },
        }));
        expect(response.status).toBe(200);
        expect(response.headers.get("location")).toBeNull();
        expect(response.headers.get("x-middleware-rewrite")).toBe(`${origin}/login?next=%2Fexample%2Fissues`);
        expect(response.headers.get("x-middleware-request-x-multica-locale")).toBe("zh-Hans");
      }
    },
  );

  it("keeps public-IP lookup local while forwarding other APIs", () => {
    vi.stubEnv("REMOTE_API_URL", "http://127.0.0.1:65534");
    expect(proxy(new NextRequest("http://localhost:3000/api/public-ip")).headers.get("x-middleware-rewrite")).toBeNull();
    expect(proxy(new NextRequest("http://localhost:3000/api/skills")).headers.get("x-middleware-rewrite")).toBe("http://127.0.0.1:65534/api/skills");
    expect(proxy(new NextRequest("http://localhost:3000/login")).headers.get("location")).toBeNull();
  });
});
