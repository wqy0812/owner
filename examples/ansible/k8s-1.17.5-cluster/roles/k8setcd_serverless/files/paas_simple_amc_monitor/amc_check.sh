#!/bin/bash
wait_sec=8
while [ "$wait_sec" -le 128 ]; do
  sleep $wait_sec
  ETCDCTL_API=3 /approot1/paas/etcd/etcdctl --user=etcd:etcd endpoint health 2>&1 1>/dev/null
  [ $? -eq 0 ] && exit 0
  wait_sec=$(( wait_sec * 2 ))
done
exit 1
