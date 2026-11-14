import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useNow } from "../lib/use-now";

afterEach(() => vi.useRealTimers());

it("updates at the requested interval and cleans up its timer", () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-11-04T00:00:00Z"));
  const initial = Date.now();
  const { result, unmount } = renderHook(({ interval }) => useNow(interval), {
    initialProps: { interval: 1000 },
  });
  expect(result.current).toBe(initial);
  act(() => vi.advanceTimersByTime(1000));
  expect(result.current).toBe(initial + 1000);
  unmount();
  expect(vi.getTimerCount()).toBe(0);
});
