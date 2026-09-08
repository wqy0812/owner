import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, unlinkSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { sourceManifest } from './source-manifest.mjs';

test('source evidence detects imported docs, fixtures, untracked files and deletions', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'clusterforge-evidence-'));
  try {
    execFileSync('git', ['init', '-q', root]);
    const inputs = ['web/src/test.ts', 'docs/platform-capabilities.md', 'examples/components/host-foundation-example.json'];
    for (const name of inputs) {
      mkdirSync(path.dirname(path.join(root, name)), { recursive: true });
      writeFileSync(path.join(root, name), 'original');
    }
    writeFileSync(path.join(root, '.gitignore'), 'output/\n');
    execFileSync('git', ['add', '.'], { cwd: root });
    let previous = sourceManifest(root).sourceSha256;
    for (const name of [...inputs, 'web/src/new.ts']) {
      writeFileSync(path.join(root, name), 'changed');
      const next = sourceManifest(root).sourceSha256;
      assert.notEqual(previous, next, name);
      previous = next;
    }
    mkdirSync(path.join(root, 'output'));
    writeFileSync(path.join(root, 'output/log'), 'evidence');
    assert.equal(previous, sourceManifest(root).sourceSha256);
    unlinkSync(path.join(root, inputs[0]));
    assert.notEqual(previous, sourceManifest(root).sourceSha256);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('browser evidence records the actual exit status for failures, cancellation and source drift', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'clusterforge-browser-evidence-'));
  try {
    const source = readFileSync(new URL('../../scripts/test-live-api-e2e.sh', import.meta.url), 'utf8');
    const summary = source.slice(source.indexOf('  python3 - "$evidence" "$result"'), source.indexOf('  chmod -R u+w "$test_root"'));
    assert.ok(summary.includes('PY_RESULT'));
    for (const code of [0, 1, 130]) for (const unchanged of [true, false]) {
      writeFileSync(path.join(root, 'source-before.json'), JSON.stringify({ sourceSha256: 'before' }));
      writeFileSync(path.join(root, 'source-after.json'), JSON.stringify({ sourceSha256: unchanged ? 'before' : 'changed' }));
      const result = spawnSync('bash', ['-c', `evidence=$1\nresult=$2\n${summary}\nexit "$result"`, 'evidence-test', root, String(code)], { encoding: 'utf8' });
      const expected = code || (unchanged ? 0 : 1);
      assert.equal(result.status, expected, result.stderr);
      assert.deepEqual(JSON.parse(readFileSync(path.join(root, 'result.json'), 'utf8')), { testExitCode: code, exitCode: expected, sourceUnchanged: unchanged });
    }
  } finally { rmSync(root, { recursive: true, force: true }); }
});
