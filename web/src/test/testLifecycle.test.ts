import { expect, it } from 'vitest';
import { deferred, finishTestTasks } from './testLifecycle';

it('aborts deferred fixture work with the consumer signal', async () => {
  const controller = new AbortController();
  const request = deferred<Response>(controller.signal);
  const result = expect(request.promise).rejects.toMatchObject({ name: 'AbortError' });
  controller.abort();
  await result;
  expect(request.settled).toBe(true);
});

it('requires an explicit opt-out to deliver an obsolete response after abort', async () => {
  const controller = new AbortController();
  const request = deferred<string>(controller.signal, { ignoreAbort: true });
  controller.abort();
  expect(request.settled).toBe(false);
  request.resolve('late response');
  await expect(request.promise).resolves.toBe('late response');
});

it('settles abandoned work at teardown and cannot overwrite an already resolved value', async () => {
  const abandoned = deferred<string>();
  const completed = deferred<string>();
  const aborted = expect(abandoned.promise).rejects.toMatchObject({ name: 'AbortError' });
  completed.resolve('saved');
  await finishTestTasks();
  await aborted;
  abandoned.resolve('too late');
  await expect(completed.promise).resolves.toBe('saved');
  expect(abandoned.settled).toBe(true);
});

it('preserves a normal fixture rejection for the consumer to observe', async () => {
  const request = deferred<string>();
  const result = expect(request.promise).rejects.toThrow('fixture failed');
  request.reject(new Error('fixture failed'));
  await result;
});
