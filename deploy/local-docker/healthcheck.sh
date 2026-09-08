#!/bin/sh
set -eu
curl -fsS --max-time 3 http://127.0.0.1:8080/api/v1/session/users >/dev/null
ssh -o BatchMode=yes -o ConnectTimeout=3 -i /var/lib/clusterforge-test/ssh/controller_ed25519 testops@127.0.0.1 true
