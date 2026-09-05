#!/usr/bin/env python3
"""Offline checks for the documentation contracts tied to this checkout."""

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
errors = []


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


design = read("docs/backend-design.md")
structure = read("docs/project-structure.md")
registered = set(re.findall(r'HandleFunc\("(\w+) /api/v1([^"?]+)', read("internal/api/handler.go")))
documented = set()
for line in design.splitlines():
    match = re.match(r"\| ([A-Z /]+) \| `([^`?]+)(?:\?[^`]*)?`", line)
    if match:
        documented.update((method, match[2]) for method in match[1].split(" / "))
for item in sorted(registered - documented):
    errors.append(f"API missing from backend-design.md: {' '.join(item)}")
for item in sorted(documented - registered):
    errors.append(f"API no longer registered: {' '.join(item)}")

contract = re.search(r'schemaContract = "([^"]+)"', read("internal/store/schema.go"))[1]
for name in ["README.md", "docs/backend-design.md", "docs/project-structure.md"]:
    contracts = set(re.findall(r"clusterforge-v1-[a-z0-9-]+", read(name)))
    if contracts != {contract}:
        errors.append(f"{name}: expected only current schema contract {contract}, found {sorted(contracts)}")

routes = {"/"} | set(re.findall(r'<Route path="([^"*]+)"', read("web/src/App.tsx")))
for route in sorted(routes):
    if f"`/{route.lstrip('/')}`" not in structure:
        errors.append(f"Page missing from project-structure.md: {route}")
api_files = [p for p in (ROOT / "internal/api").glob("*.go") if not p.name.endswith("_test.go")]
for path in api_files:
    if f"`{path.name}`" not in structure:
        errors.append(f"API file missing from project-structure.md: {path.name}")

link_count = 0
for path in [ROOT / "README.md", *sorted((ROOT / "docs").rglob("*.md"))]:
    source = re.sub(r"```.*?```", "", path.read_text(encoding="utf-8"), flags=re.S)
    # Inline links used in these docs; allow one nested pair for Chinese filenames.
    for match in re.finditer(r"\[[^\]\n]*\]\((<[^>]+>|(?:[^()\s]|\([^()]*\))+)(?:\s+\"[^\"]*\")?\)", source):
        target = match[1].strip("<>")
        parts = urlsplit(target)
        if parts.scheme or parts.netloc or not parts.path:
            continue
        resolved = path.parent / unquote(parts.path)
        if not resolved.exists():
            errors.append(f"{path.relative_to(ROOT)}: broken relative file link {target}")
        link_count += 1

if errors:
    print("Documentation checks failed:\n" + "\n".join(f"- {error}" for error in errors), file=sys.stderr)
    sys.exit(1)
print(f"Documentation checks passed: {len(registered)} API methods/paths, {len(routes)} page routes, "
      f"{len(api_files)} API source files, {link_count} relative links; schema {contract}")
