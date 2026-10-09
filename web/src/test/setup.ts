import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { beforeEach, afterEach } from "vitest";
afterEach(cleanup);

beforeEach(() => history.replaceState(null, "", "/"));

// Recharts observes its container; jsdom has no layout engine.
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
