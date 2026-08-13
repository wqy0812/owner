#!/bin/bash
set -o pipefail

IP=$(hostname -i)

KUBELET_STAT=$(curl http://localhost:10248/healthz 2>/dev/null)

if [ "$KUBELET_STAT" == "ok" ]; then
	exit 0
else
	exit 1
fi