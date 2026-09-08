import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lstatSync, readFileSync, readlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Include repository inputs outside web/src (documentation and JSON fixtures),
// plus untracked source. Git-ignored build outputs and evidence are excluded.
export function sourceManifest(root) {
  const names = execFileSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'], { cwd: root }).toString().split('\0').filter(Boolean);
  const files = {};
  for (const name of [...new Set(names)].sort()) {
    const filename = path.join(root, name);
    try {
      const stat = lstatSync(filename);
      const content = stat.isSymbolicLink() ? readlinkSync(filename) : readFileSync(filename);
      files[name] = createHash('sha256').update(String(stat.mode)).update('\0').update(content).digest('hex');
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
      files[name] = 'deleted';
    }
  }
  return { sourceSha256: createHash('sha256').update(JSON.stringify(files)).digest('hex'), files };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const manifest = JSON.stringify(sourceManifest(path.resolve(fileURLToPath(new URL('../..', import.meta.url)))), null, 2) + '\n';
  if (process.argv[2]) writeFileSync(process.argv[2], manifest);
  else process.stdout.write(manifest);
}
