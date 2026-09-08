// Disable RTL's automatic unmount so the owned teardown can wrap it in act.
import '@testing-library/react/dont-cleanup-after-each';
import '@testing-library/jest-dom/vitest';
import { act, cleanup } from '@testing-library/react';
import { createRequire } from 'node:module';
import { useId } from 'react';
import { afterAll, afterEach, beforeEach, expect, vi } from 'vitest';
import { finishTestTasks, unexpectedRequests } from './testLifecycle';

// Ant's CJS dependency bypasses vi.mock and deliberately returns "test-id".
// Replace only this hook with React's real IDs; keep every library in test mode.
const requireFromWeb = createRequire(import.meta.url);
const antRequire = createRequire(requireFromWeb.resolve('antd'));
const idHook = antRequire('@rc-component/util/lib/hooks/useId') as { default: (id?: string) => string };
const originalIdHook = idHook.default;
idHook.default = (id?: string) => { const generated = useId(); return id || generated; };
afterAll(() => { idHook.default = originalIdHook; });

const originalError = console.error;
const originalWarn = console.warn;
let asyncWarnings: string[] = [];
beforeEach(() => {
  asyncWarnings = [];
  unexpectedRequests.length = 0;
  const capture = (original: typeof console.error) => (...args: unknown[]) => {
    const text = args.map(String).join(' ');
    if (/overlapping act\(\)|not (?:wrapped in|configured to support) act\(|act\(async.*without await|Cannot update a component.*while rendering|Can't perform a React state update|Maximum update depth exceeded/.test(text)) asyncWarnings.push(text);
    original(...args);
  };
  console.error = capture(originalError);
  console.warn = capture(originalWarn);
});

afterEach(async () => {
  try {
    await act(async () => { cleanup(); await finishTestTasks(); });
    const openStreams = EventSourceMock.instances.filter(stream => stream.readyState !== EventSourceMock.CLOSED);
    for (const stream of EventSourceMock.instances) stream.close();
    expect(openStreams, 'SSE streams must close on unmount').toHaveLength(0);
    expect(unexpectedRequests, `All fixture requests need an explicit handler:\n${unexpectedRequests.join('\n')}`).toEqual([]);
    expect(asyncWarnings, 'React asynchronous work must settle within its test').toEqual([]);
  } finally {
    EventSourceMock.instances.length = 0;
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    localStorage.clear();
    sessionStorage.clear();
    console.error = originalError;
    console.warn = originalWarn;
  }
});

class ResizeObserverMock {
  observe() { }
  unobserve() { }
  disconnect() { }
}

export class EventSourceMock {
  static instances: EventSourceMock[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSED = 2;
  readonly url: string;
  readonly withCredentials = true;
  readyState = 1;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  private readonly listeners = new Map<string, Set<EventListener>>();

  constructor(url: string | URL) {
    this.url = String(url);
    EventSourceMock.instances.push(this);
  }
  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const callback: EventListener = typeof listener === 'function' ? listener : (event) => listener.handleEvent(event);
    const callbacks = this.listeners.get(type) ?? new Set<EventListener>();
    callbacks.add(callback);
    this.listeners.set(type, callbacks);
  }
  removeEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const callback: EventListener = typeof listener === 'function' ? listener : (event) => listener.handleEvent(event);
    this.listeners.get(type)?.delete(callback);
  }
  dispatchEvent() { return true; }
  close() { this.readyState = 2; this.listeners.clear(); this.onopen = this.onmessage = this.onerror = null; }
  emit(type: string, data: unknown = {}) {
    if (this.readyState === EventSourceMock.CLOSED) return;
    const event = new MessageEvent(type, { data: JSON.stringify(data) });
    if (type === 'message') this.onmessage?.(event);
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

Object.defineProperty(globalThis, 'ResizeObserver', { value: ResizeObserverMock, writable: true });
Object.defineProperty(globalThis, 'EventSource', { value: EventSourceMock, writable: true });
Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { value: vi.fn(), writable: true });
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }),
});

// jsdom does not implement pseudo-element computed styles used for scrollbar measurement.
const originalComputedStyle = window.getComputedStyle;
window.getComputedStyle = (element) => originalComputedStyle(element);
