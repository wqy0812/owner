#!/bin/bash
set -o pipefail

IP=$(hostname -i)

KUBEAPISERVER_STAT=$(curl https://${IP}:6443/healthz --key /approot1/paas/kube/cert/admin-key.pem  --cert /approot1/paas/kube/cert/admin.pem --cacert /approot1/paas/kube/cert/cb-ca.pem 2>/dev/null)

if [ "$KUBEAPISERVER_STAT" == "ok" ]; then
	exit 0
else
	exit 1
fi