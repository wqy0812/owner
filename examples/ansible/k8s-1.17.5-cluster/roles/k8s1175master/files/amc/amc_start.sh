#!/bin/bash
BASE_DIR=$(cd `dirname $0`;pwd)
BASE_FILE=$(basename $0)
source ${BASE_DIR}/amc.properties
nohup $BASE_DIR/amc_monitor.sh $BASE_DIR 1>>$BASE_DIR/amc.log 2>&1 &
sleep 1 
exit 0

