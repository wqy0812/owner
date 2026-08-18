#!/bin/bash
BASE_DIR=$1
amcFunc(){
  sleep 150
  amc_es_pid=`ps -ef|grep amc_monitor.sh|sed '/grep/d'|awk '{print $2}'`
  if [ -z "$amc_es_pid" ]; then
    echo amc_monitor is down.
    nohup $BASE_DIR/amc_monitor.sh $BASE_DIR 1>$ENV_LOG_PATH/amc.log 2>&1 &
  else
    echo amc_monitor is up.
  fi
  sleep 150
}
while true
do
  amcFunc > $ENV_LOG_PATH/amc_shadow.log
done
