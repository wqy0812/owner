#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { closeSync, mkdirSync, openSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Run the same source five times without retries, skips or timeout overrides.
const web = fileURLToPath(new URL('..', import.meta.url));
const root = path.dirname(web);
const output = path.resolve(process.argv[2] ?? path.join(root, 'output/playwright/frontend-test-stability', new Date().toISOString().replaceAll(':', '-')));
mkdirSync(output, { recursive: true });
const seed = 20260907;
const runs = [
  ...[1, 2, 3].map(number => ({ name: `ordinary-${number}`, args: [] })),
  { name: 'coverage-1', args: ['--coverage'] },
  { name: 'coverage-shuffled', args: ['--coverage', '--sequence.shuffle', `--sequence.seed=${seed}`] },
];
function sourceHash() {
  const files = [];
  function visit(directory) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const filename = path.join(directory, entry.name);
      if (entry.isDirectory()) visit(filename);
      else files.push(filename);
    }
  }
  visit(path.join(web, 'src'));
  files.push(...['vitest.config.ts', 'package.json', 'pnpm-lock.yaml', 'scripts/test-stability.mjs'].map(file => path.join(web, file)));
  const hash = createHash('sha256');
  for (const file of files.sort()) hash.update(path.relative(web, file)).update('\0').update(readFileSync(file)).update('\0');
  return hash.digest('hex');
}
const manifest = {
  startedAt: new Date().toISOString(), sourceSha256: sourceHash(), node: process.version,
  pnpm: spawnSync('pnpm', ['--version'], { encoding: 'utf8', cwd: web }).stdout.trim(),
  gitHead: spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8', cwd: root }).stdout.trim(),
  seed, config: readFileSync(path.join(web, 'vitest.config.ts'), 'utf8'), runs: [],
};
const save = () => writeFileSync(path.join(output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
save();
for (const run of runs) {
  const args = ['exec', 'vitest', 'run', ...run.args, '--reporter=verbose', '--reporter=json', `--outputFile=${path.join(output, `${run.name}.json`)}`];
  if (run.args.includes('--coverage')) args.push(`--coverage.reportsDirectory=${path.join(output, run.name)}`);
  console.log(`Starting ${run.name}; log: ${path.join(output, `${run.name}.log`)}`);
  const log = openSync(path.join(output, `${run.name}.log`), 'w');
  const start = performance.now();
  const result = spawnSync('pnpm', args, { cwd: web, stdio: ['ignore', log, log] });
  closeSync(log);
  const record = { name: run.name, command: ['pnpm', ...args], seconds: (performance.now() - start) / 1000, exitCode: result.status, signal: result.signal, sourceUnchanged: sourceHash() === manifest.sourceSha256 };
  manifest.runs.push(record);
  save();
  console.log(`${run.name}: exit=${result.status}, ${record.seconds.toFixed(2)}s, source unchanged=${record.sourceUnchanged}`);
  if (result.error || result.status !== 0 || !record.sourceUnchanged) {
    if (result.error) console.error(result.error);
    process.exitCode = 1;
    break;
  }
}
manifest.finishedAt = new Date().toISOString();
save();
