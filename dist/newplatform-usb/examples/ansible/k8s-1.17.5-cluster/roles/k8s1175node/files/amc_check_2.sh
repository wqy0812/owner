#!/bin/bash
if curl -sS -m 5 localhost:10249/healthz|grep -q -w "ok" ||
   curl -sS -m 5 $(hostname -i):10249/healthz|grep -q -w "ok"
then
  exit 0
else
  echo kube-proxy is not healthy
  exit 1
fi