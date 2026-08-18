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

reportFunc(){
    #开始检查进程可用性
    TM=`date +%Y/%m/%d-%H:%M:%S`
    normalModule=()
    abnormalModule=()
    echo "enabled shell: ${EnabledCheckShell}"
    echo "retry interval: ${RetryInterval}"
    echo "max retry count: ${MaxRetryCount}"

    for checkShell in ${EnabledCheckShell};
    do
        #retry
        count=1
        while [[ ${count} < ${MaxRetryCount} ]]
        do
            echo "start the num: ${count} time to check: amc_check_${checkShell}.sh"
            if ${BASE_DIR}/amc_check_${checkShell}.sh;then
                echo "amc_check_${checkShell}.sh is normal, execute next shell check"
                eval normalModule[${#normalModule[*]}]=\${ENV_AMC_SUBMODULECODE_${checkShell}}
                break
            else
                echo "amc_check_${checkShell}.sh is abnormal, retry the num: ${count} time"
                if [[ $[${count}+1] == ${MaxRetryCount} ]]; then
                    eval abnormalModule[${#abnormalModule[*]}]=\${ENV_AMC_SUBMODULECODE_${checkShell}}
                fi
            fi
            count=$[${count}+1]
            sleep ${RetryInterval}
        done
    done
    #send msg once
    echo "abnormalModule len: ${#abnormalModule[*]}"
    if [[ ${#abnormalModule[*]} > 0 ]]; then
        message="[${TM}] running normally module: ${normalModule[*]}, running abnormally module: ${abnormalModule[*]}"
        echo ${message}
        echo "<?xml version=\"1.0\" encoding=\"GBK\" ?><APPROOT type=\"BAMC_USABILITY_REPORT\" ver=\"2.0\" from =\"F-PAAS\" to=\"F-AMC\"  mode=\"asy\"><PUBLIC><APPSNAME>${ENV_APP_NAME}</APPSNAME><TYPE>02</TYPE><IP>${IP}</IP></PUBLIC><PRIVATE><ROWSET><ROW><OCCUREDAPPSNAME>${ENV_APP_NAME}</OCCUREDAPPSNAME><OCCUREDIP>${IP}</OCCUREDIP><MODULECODE>${ENV_AMC_MODULECODE_MASTER}</MODULECODE><SUBMODULECODE>${ENV_AMC_SUBMODULECODE_3}</SUBMODULECODE><STATUS>2</STATUS><MSG>${message}</MSG></ROW></ROWSET></PRIVATE></APPROOT>" 1>&8
    else
        message="[${TM}] all module running normally: ${normalModule[*]}"
        echo ${message}
        echo "<?xml version=\"1.0\" encoding=\"GBK\" ?><APPROOT type=\"BAMC_USABILITY_REPORT\" ver=\"2.0\" from =\"F-PAAS\" to=\"F-AMC\"  mode=\"asy\"><PUBLIC><APPSNAME>${ENV_APP_NAME}</APPSNAME><TYPE>02</TYPE><IP>${IP}</IP></PUBLIC><PRIVATE><ROWSET><ROW><OCCUREDAPPSNAME>${ENV_APP_NAME}</OCCUREDAPPSNAME><OCCUREDIP>${IP}</OCCUREDIP><MODULECODE>${ENV_AMC_MODULECODE_MASTER}</MODULECODE><SUBMODULECODE>${ENV_AMC_SUBMODULECODE_3}</SUBMODULECODE><STATUS>0</STATUS><MSG>${message}</MSG></ROW></ROWSET></PRIVATE></APPROOT>" 1>&8
    fi
}

while true
do
	amc_shadow_pid=`ps -ef|grep amc_shadow.sh|sed '/grep/d'|awk '{print $2}'`
	if [ -z "$amc_shadow_pid" ]; then
		echo amc_shadow is down.
		nohup $BASE_DIR/amc_shadow.sh $BASE_DIR 1>>$BASE_DIR/amc.log 2>&1 &
	else
		echo amc_shadow is up.
	fi
#start run checks
	reportFunc
	echo "wait for the next check loop..."
	sleep 300
done
