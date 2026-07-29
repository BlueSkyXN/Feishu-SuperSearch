import "@testing-library/jest-dom/vitest";
import { vi } from "vitest";

Object.defineProperty(window, "scrollTo", { value: vi.fn(), writable: true });
Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { value: vi.fn(), writable: true });
Object.defineProperty(window, "confirm", { value: vi.fn(() => true), writable: true });
Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
  value: vi.fn(() => ({ measureText: () => ({ width: 0 }) })),
  writable: true,
});
