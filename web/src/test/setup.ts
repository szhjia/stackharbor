import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { beforeEach, afterEach } from "vitest";
afterEach(cleanup);

beforeEach(() => history.replaceState(null, "", "/"));
