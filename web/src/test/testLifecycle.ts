const pending = new Set<() => void>();
export const unexpectedRequests: string[] = [];

/** Deferred fixture work is owned by its test, including intentionally unabortable late responses. */
export function deferred<T>(signal?: AbortSignal | null, options: { ignoreAbort?: boolean } = {}) {
  let resolvePromise!: (value: T | PromiseLike<T>) => void;
  let rejectPromise!: (reason: unknown) => void;
  let settled = false;
  const promise = new Promise<T>((resolve, reject) => { resolvePromise = resolve; rejectPromise = reject; });
  const release = () => { settled = true; pending.delete(dispose); signal?.removeEventListener('abort', abort); };
  const resolve = (value: T | PromiseLike<T>) => { if (!settled) { release(); resolvePromise(value); } };
  const reject = (reason: unknown) => { if (!settled) { release(); rejectPromise(reason); } };
  const abort = () => reject(new DOMException('Fixture request aborted', 'AbortError'));
  const dispose = () => {
    // Only teardown cancellation is consumed here; normal test failures remain observable.
    void promise.catch(() => { });
    abort();
  };
  pending.add(dispose);
  if (!options.ignoreAbort) {
    if (signal?.aborted) abort();
    else signal?.addEventListener('abort', abort, { once: true });
  }
  return { promise, resolve, reject, get settled() { return settled; } };
}

export async function finishTestTasks() {
  for (const dispose of [...pending]) dispose();
  await Promise.resolve();
}

export function pendingResponse(
  register: (resolve: (response: Response | PromiseLike<Response>) => void) => void,
  signal?: AbortSignal | null,
  options?: { ignoreAbort?: boolean },
) {
  const request = deferred<Response>(signal, options);
  register(request.resolve);
  return request.promise;
}

export function deferredTask<T>(
  register: (resolve: (value: T | PromiseLike<T>) => void, reject: (reason: unknown) => void) => void,
  signal?: AbortSignal | null,
  options?: { ignoreAbort?: boolean },
) {
  const task = deferred<T>(signal, options);
  register(task.resolve, task.reject);
  return task.promise;
}

export function unexpectedRequest(input: RequestInfo | URL, init?: RequestInit): never {
  const message = `Unexpected fixture request: ${init?.method ?? 'GET'} ${String(input)}`;
  unexpectedRequests.push(message);
  throw new Error(message);
}
