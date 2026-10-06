import type { Inventory, Plan, Operation, LogPage } from "./types";
export class ApiFailure extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}
export class ApiClient {
  private csrf = "";
  private connecting?: Promise<{ csrf: string; expires_at: string }>;
  constructor(private fetcher: typeof fetch = fetch.bind(globalThis)) {}
  async request<T>(path: string, body?: unknown, recoverSession = true): Promise<T> {
    let response: Response;
    let envelope: { data?: T; error?: { code: string; message: string } };
    try {
      response = await this.fetcher("/api/v1/" + path, {
        method: body === undefined ? "GET" : "POST",
        credentials: "same-origin",
        headers:
          body === undefined
            ? {}
            : { "Content-Type": "application/json", "X-CSRF-Token": this.csrf },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(11000),
      });
      envelope = await response.json();
      if (
        !envelope ||
        typeof envelope !== "object" ||
        !("data" in envelope || "error" in envelope)
      )
        throw new TypeError("Invalid API response envelope");
    } catch {
      throw new ApiFailure(
        0,
        "outcome_unknown",
        body === undefined
          ? "Connection unavailable. Reconnect to refresh."
          : "Request outcome unknown. The action may already be executing. Inspect Operations before any new action.",
      );
    }
    if (!response.ok || envelope.error) {
      // A rejected unauthenticated request has not been accepted for execution.
      // Renew local request protection once; never retry unknown outcomes.
      if (response.status === 401 && path !== "auth/session" && recoverSession) {
        await this.session();
        return this.request<T>(path, body, false);
      }
      const e = envelope.error;
      throw new ApiFailure(
        response.status,
        e?.code ?? "unavailable",
        e?.message ?? "Request failed",
      );
    }
    return envelope.data as T;
  }
  session() {
    if (!this.connecting) {
      this.connecting = this.request<{ csrf: string; expires_at: string }>(
        "auth/session",
      ).then((s) => {
        this.csrf = s.csrf;
        return s;
      }).finally(() => {
        this.connecting = undefined;
      });
    }
    return this.connecting;
  }
  async bootstrap(url: URL, replace: (path: string) => void) {
    const token = new URLSearchParams(url.hash.slice(1)).get("token");
    if (token) {
      replace(url.pathname + url.search);
    }
    return this.session();
  }
  inventory() {
    return this.request<Inventory>("inventory");
  }
  sessionPath(sid: string, path: string) {
    return `sessions/${encodeURIComponent(sid)}/${path}`;
  }
  plan(sid: string, action: string, targets: string[], port?: number) {
    return this.request<Plan>(this.sessionPath(sid, "plans"), {
      action,
      targets,
      ...(port ? { port } : {}),
    });
  }
  submit(p: Plan, key: string) {
    return this.request<Operation>(
      this.sessionPath(p.session_id, "operations"),
      { plan_id: p.id, idempotency_key: key },
    );
  }
  operations(sid: string) {
    return this.request<Operation[]>(this.sessionPath(sid, "operations"));
  }
  operation(sid: string, id: string) {
    return this.request<Operation>(
      this.sessionPath(sid, `operations/${encodeURIComponent(id)}`),
    );
  }
  cancel(sid: string, id: string) {
    return this.request(
      this.sessionPath(sid, `operations/${encodeURIComponent(id)}/cancel`),
      {},
    );
  }
  logs(sid: string, target: string, cursor = "") {
    return this.request<LogPage>(
      this.sessionPath(sid, "logs") +
        "?" +
        new URLSearchParams({ target, cursor, limit: "500" }),
    );
  }
}
export const api = new ApiClient();
export const newKey = (p: Plan) => `${p.id}:${crypto.randomUUID()}`;
export function actionableError(e: unknown) {
  if (e instanceof ApiFailure && [409, 410].includes(e.status))
    return (
      "Plan changed or expired. Review the current state and plan again. " +
      e.message
    );
  return e instanceof Error ? e.message : "Request failed";
}
