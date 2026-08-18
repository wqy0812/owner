#!/bin/bash
set -o pipefail

IP=$(hostname -i)

KUBEPROXY_STAT=$(curl http://${IP}:10249/healthz 2>/dev/null)

if [ "$KUBEPROXY_STAT" == "ok" ]; then
	exit 0
else
	exit 1
fi