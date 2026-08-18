#!/bin/bash
set -o pipefail

IP=$(hostname -i)

KUBESCHEDULER_STAT=$(curl http://127.0.0.1:10251/healthz 2>/dev/null)

if [ "$KUBESCHEDULER_STAT" == "ok" ]; then
	exit 0
else
	exit 1
fi