import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MetricView } from "../components/MetricView";
import { PlanDialog } from "../components/PlanDialog";
import { LogView } from "../components/LogView";
import { ApiClient, ApiFailure } from "../api/client";
import { mergeLogPage } from "../api/logs";
const metric = {
  known: false,
  partial: false,
  rss_bytes: null,
  cpu_percent: null,
  uptime_millis: null,
  sampled_at: null,
};
const plan = {
  id: "p1",
  session_id: "s1",
  action: "restart",
  targets: ["app/api"],
  affected: ["app/api", "app/worker"],
  warnings: ["Shared dependency coverage is partial"],
  expires_at: "2099-01-01T00:00:00Z",
  fingerprint: "f",
};
describe("browser contracts", () => {
  it("never shows unknown CPU/RSS as zero", () => {
    render(<MetricView metric={metric} />);
    expect(screen.getAllByText("Unknown")).toHaveLength(2);
  });
  it("shows complete impact and blocks duplicate confirmation", async () => {
    const submit = vi.fn(() => new Promise<void>(() => {}));
    render(
      <PlanDialog
        plans={[{ root: "/work", plan }]}
        onSubmit={submit}
        onClose={() => {}}
      />,
    );
    expect(screen.getByText("app/worker")).toBeVisible();
    expect(screen.getByText(plan.warnings[0])).toBeVisible();
    const b = screen.getByRole("button", { name: "Confirm restart" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
  });
  it("409 requires explicit replan", async () => {
    render(
      <PlanDialog
        plans={[{ root: "/work", plan }]}
        onSubmit={async () => {
          throw new ApiFailure(409, "plan_conflict", "Changed");
        }}
        onClose={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Confirm restart" }));
    expect(await screen.findByText(/Plan changed.*plan again/)).toBeVisible();
  });
  it("renders HTML log payload as text and gap explicitly", () => {
    render(
      <LogView
        entries={[
          {
            sequence: 1,
            time: "2026-10-05T00:00:00Z",
            project_id: "app",
            service_id: "app/api",
            stream: "stdout",
            text: "<img src=x onerror=alert(1)>",
          },
        ]}
        gap={3}
        connected={true}
      />,
    );
    expect(screen.getByText("<img src=x onerror=alert(1)>")).toBeVisible();
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText(/3.*entries.*unavailable/)).toBeVisible();
  });
  it("valid cookie reload bootstraps session without a token", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(
        new Response(
          JSON.stringify({ data: { csrf: "csrf", expires_at: "2099" } }),
        ),
      );
    const client = new ApiClient(fetcher);
    await client.bootstrap(new URL("http://127.0.0.1/"), vi.fn());
    expect(fetcher.mock.calls[0][0]).toBe("/api/v1/auth/session");
  });
  it("clears fragment even on failed exchange and preserves rerun guidance", async () => {
    const replace = vi.fn();
    const client = new ApiClient(
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: "unauthenticated",
              message: "run stackharbor web again",
            },
          }),
          { status: 401 },
        ),
      ),
    );
    await expect(
      client.bootstrap(new URL("http://127.0.0.1/#token=secret"), replace),
    ).rejects.toThrow("run stackharbor web again");
    expect(replace).toHaveBeenCalledWith("/");
  });
  it("deduplicates gap and log pages by sequence within exact filter", () => {
    const entry = {
      sequence: 1,
      time: "now",
      project_id: "app",
      service_id: "app/api",
      stream: "stdout",
      text: "one",
    };
    const p = {
      session_id: "s",
      target: "app/api",
      entries: [entry],
      gap: true,
      dropped: 2,
      reset: false,
      next_cursor: 1,
      cursor: "c",
    };
    const once = mergeLogPage([], p);
    expect(mergeLogPage(once, p)).toHaveLength(1);
  });
});

describe("incomplete accepted responses", () => {
  it.each(["truncated JSON", "rejected body read"])(
    "classifies %s as an unknown accepted outcome",
    async (mode) => {
      const response =
        mode === "truncated JSON"
          ? new Response('{"data":{"id":"accepted"', { status: 202 })
          : ({
              ok: true,
              status: 202,
              json: vi
                .fn()
                .mockRejectedValue(new TypeError("stream interrupted")),
            } as unknown as Response);
      const client = new ApiClient(vi.fn().mockResolvedValue(response));
      await expect(client.submit(plan, "p1:nonce")).rejects.toMatchObject({
        code: "outcome_unknown",
        message: expect.stringMatching(/may already be executing.*Operations/),
      });
    },
  );
  it("shows may-already-execute guidance without allowing another submit", async () => {
    const client = new ApiClient(
      vi.fn().mockResolvedValue(new Response('{"data":', { status: 202 })),
    );
    render(
      <PlanDialog
        plans={[{ root: "/work", plan }]}
        onSubmit={async () => {
          await client.submit(plan, "p1:nonce");
        }}
        onClose={() => {}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Confirm restart" }));
    expect(
      await screen.findByText(/may already be executing.*Operations/),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Confirm restart" }),
    ).toBeDisabled();
  });
  it("preserves a fully received structured conflict", async () => {
    const client = new ApiClient(
      vi
        .fn()
        .mockResolvedValue(
          new Response(
            JSON.stringify({
              error: { code: "plan_conflict", message: "Changed" },
            }),
            { status: 409 },
          ),
        ),
    );
    await expect(client.submit(plan, "p1:nonce")).rejects.toMatchObject({
      status: 409,
      code: "plan_conflict",
      message: "Changed",
    });
  });
});
