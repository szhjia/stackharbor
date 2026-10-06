import { useRef, useState, useEffect } from "react";
import type { Plan } from "../api/types";
import { actionableError } from "../api/client";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "./ui/dialog";
import { Button } from "./ui/button";
import { Notice } from "./Notice";
export type Planned = { root: string; plan: Plan };
export function PlanDialog({
  plans,
  onSubmit,
  onClose,
}: {
  plans: Planned[];
  onSubmit: () => Promise<void>;
  onClose: () => void;
}) {
  const previousFocus = useRef(document.activeElement as HTMLElement | null);
  const focusKey = useRef(previousFocus.current?.dataset.focusKey);
  const locked = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const expired = plans.some((p) => Date.parse(p.plan.expires_at) <= now);
  const action = plans[0]?.plan.action ?? "action";
  async function submit() {
    if (locked.current || expired) return;
    locked.current = true;
    setBusy(true);
    try {
      await onSubmit();
      onClose();
    } catch (e) {
      setError(actionableError(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent
        className="plan-dialog"
        showCloseButton={!busy}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          const current = focusKey.current
            ? Array.from(
                document.querySelectorAll<HTMLElement>("[data-focus-key]"),
              ).find((e) => e.dataset.focusKey === focusKey.current)
            : previousFocus.current;
          if (current?.isConnected) current.focus();
        }}
      >
        <DialogHeader>
          <DialogTitle>Review {action}</DialogTitle>
          <DialogDescription>
            {plans.length} independent session{" "}
            {plans.length === 1 ? "plan" : "plans"}. Review every affected node
            before confirming.
          </DialogDescription>
        </DialogHeader>
        <div className="plan-scroll">
          {plans.map(({ root, plan }) => (
            <section key={plan.id} className="plan-section">
              <h3>{root}</h3>
              <small className="mono">Session {plan.session_id}</small>
              <p>Requested targets</p>
              <ul>
                {plan.targets.map((id) => (
                  <li key={id} className="mono">
                    {id}
                  </li>
                ))}
              </ul>
              <p>Full affected set</p>
              <ul>
                {plan.affected.map((id) => (
                  <li key={id} className="mono">
                    {id}
                  </li>
                ))}
              </ul>
              {plan.warnings.map((w, i) => (
                <Notice key={i} title="Caution">
                  {w}
                </Notice>
              ))}
              <p className="caption">
                Expires {new Date(plan.expires_at).toLocaleTimeString()} ·{" "}
                {Math.max(
                  0,
                  Math.ceil((Date.parse(plan.expires_at) - now) / 1000),
                )}
                s remaining
              </p>
            </section>
          ))}
        </div>
        {expired ? (
          <Notice title="Plan expired">
            Close this review and plan again.
          </Notice>
        ) : null}
        {error ? (
          <Notice title="Action requires attention" danger>
            {error}
          </Notice>
        ) : null}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {error ? "Close" : "Cancel"}
          </Button>
          <Button disabled={busy || expired || Boolean(error)} onClick={submit}>
            {busy ? "Submitting…" : `Confirm ${action}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
