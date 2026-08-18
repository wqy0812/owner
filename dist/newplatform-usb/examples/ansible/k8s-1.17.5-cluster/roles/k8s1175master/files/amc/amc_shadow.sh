#!/bin/bash
BASE_DIR=$1
while true
do
  sleep 150
  amc_es_pid=`ps -ef|grep amc_monitor.sh|sed '/grep/d'|awk '{print $2}'`
  if [ -z "$amc_es_pid" ]; then
    echo amc_monitor is down.
    nohup $BASE_DIR/amc_monitor.sh $BASE_DIR 1>>$BASE_DIR/amc.log 2>&1 &
  else
    echo amc_monitor is up.
  fi
  sleep 150
done
