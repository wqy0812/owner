#!/bin/sh
set -eu

scan_test_sources() {
  find . -type f \
    ! -path './.git/*' \
    ! -path './output/*' \
    ! -path './web/node_modules/*' \
    \( \
      -name '*_test.go' \
      -o -path './web/src/test/*' \
      -o -path './web/e2e/*' \
      -o -path './scripts/test-*.sh' \
    \) \
    -exec grep -EnH "$1" {} + 2>/dev/null || true
}

private_network_matches=$(scan_test_sources '(^|[^0-9])(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3})([^0-9]|$)')
if [ -n "$private_network_matches" ]; then
  printf '%s\n' 'test fixture boundary violation: private network address found' >&2
  printf '%s\n' "$private_network_matches" >&2
  exit 1
fi

persona_matches=$(scan_test_sources '(component-alice|component-bob|scenario-carol|environment-dave)')
if [ -n "$persona_matches" ]; then
  printf '%s\n' 'test fixture boundary violation: seeded persona identifier found' >&2
  printf '%s\n' "$persona_matches" >&2
  exit 1
fi

demo_matches=$(scan_test_sources '(^|[^[:alnum:]])[Dd][Ee][Mm][Oo]([^[:alnum:]]|$)' | grep -v 'codex/platform-demo/' || true)
if [ -n "$demo_matches" ]; then
  printf '%s\n' 'test fixture boundary violation: demo-specific literal found' >&2
  printf '%s\n' "$demo_matches" >&2
  exit 1
fi

printf '%s\n' 'test fixture boundary passed'
