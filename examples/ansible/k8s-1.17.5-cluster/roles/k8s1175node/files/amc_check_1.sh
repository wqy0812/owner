#!/bin/bash

if curl -sS -m 5 localhost:10248/healthz|grep -q -w "ok" ||
   curl -sS -m 5 $(hostname -i):10248/healthz|grep -q -w "ok"
then
  exit 0
else
  echo kubelet is not healthy
  exit 1
fi