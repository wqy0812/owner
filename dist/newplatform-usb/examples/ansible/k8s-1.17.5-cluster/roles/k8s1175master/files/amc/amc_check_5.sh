#!/bin/bash
set -o pipefail

IP=$(hostname -i)

KUBECONTROLLERMANAGER_STAT=$(curl --cacert /approot1/paas/kube/cert/cb-ca.pem --cert /approot1/paas/kube/cert/kube-controller-manager/kube-controller-manager.pem --key /approot1/paas/kube/cert/kube-controller-manager/kube-controller-manager-key.pem https://127.0.0.1:10252/healthz 2>/dev/null)

if [ "$KUBECONTROLLERMANAGER_STAT" == "ok" ]; then
	exit 0
else
	exit 1
fi