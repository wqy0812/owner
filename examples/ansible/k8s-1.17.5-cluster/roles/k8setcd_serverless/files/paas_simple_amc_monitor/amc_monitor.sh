#!/bin/bash

BASE_DIR=$1

exec 8<>/dev/udp/${ENV_AMC_USABLITY_ADDR}/${ENV_AMC_USABLITY_PORT}
if [ $? -eq 0 ]; then
	echo connect to AMC successfully...
else
	echo connect to AMC failed.
	exit 1
fi
#获取IP地址
IP=`hostname -i`

while true
do
	amc_shadow_pid=`ps -ef|grep amc_shadow.sh|sed '/grep/d'|awk '{print $2}'`
	if [ -z "$amc_shadow_pid" ]; then
		echo amc_shadow is down.
		nohup $BASE_DIR/amc_shadow.sh $BASE_DIR 1>>$BASE_DIR/amc.log 2>&1 &
	else
		echo amc_shadow is up.
	fi

	TM=`date +%Y/%m/%d-%H:%M:%S`
	#开始检查进程可用性
	Nmsg_1="[$TM][Nomarl]ETCD is running nomarlly."
	UNmsg_1="[$TM][Critical]ETCD is running abnomarlly."
	$BASE_DIR/amc_check.sh && \
	echo ${Nmsg_1} && \
	echo "<?xml version=\"1.0\" encoding=\"GBK\" ?><APPROOT type=\"BAMC_USABILITY_REPORT\" ver=\"2.0\" from =\"F-PAAS\" to=\"F-AMC\"  mode=\"asy\"><PUBLIC><APPSNAME>${ENV_APP_NAME}</APPSNAME><TYPE>02</TYPE><IP>${IP}</IP></PUBLIC><PRIVATE><ROWSET><ROW><OCCUREDAPPSNAME>${ENV_APP_NAME}</OCCUREDAPPSNAME><OCCUREDIP>${IP}</OCCUREDIP><MODULECODE>${ENV_AMC_MODULECODE}</MODULECODE><SUBMODULECODE></SUBMODULECODE><STATUS>0</STATUS><MSG>${Nmsg_1}</MSG></ROW></ROWSET></PRIVATE></APPROOT>" 1>&8 || \
	{ \
		echo ${UNmsg_1} && \
		echo "<?xml version=\"1.0\" encoding=\"GBK\" ?><APPROOT type=\"BAMC_USABILITY_REPORT\" ver=\"2.0\" from =\"F-PAAS\" to=\"F-AMC\"  mode=\"asy\"><PUBLIC><APPSNAME>${ENV_APP_NAME}</APPSNAME><TYPE>02</TYPE><IP>${IP}</IP></PUBLIC><PRIVATE><ROWSET><ROW><OCCUREDAPPSNAME>${ENV_APP_NAME}</OCCUREDAPPSNAME><OCCUREDIP>${IP}</OCCUREDIP><MODULECODE>${ENV_AMC_MODULECODE}</MODULECODE><SUBMODULECODE></SUBMODULECODE><STATUS>2</STATUS><MSG>${UNmsg_1}</MSG></ROW></ROWSET></PRIVATE></APPROOT>" 1>&8; \
	}
	sleep 300
done
