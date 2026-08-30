import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach, vi } from 'vitest';

afterEach(() => {
  cleanup();
  EventSourceMock.instances.length = 0;
});

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
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
  close() { this.readyState = 2; }
  emit(type: string) {
    const event = new MessageEvent(type);
    if (type === 'message') this.onmessage?.(event);
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

Object.defineProperty(globalThis, 'ResizeObserver', { value: ResizeObserverMock, writable: true });
Object.defineProperty(globalThis, 'EventSource', { value: EventSourceMock, writable: true });
Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { value: vi.fn(), writable: true });
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});
