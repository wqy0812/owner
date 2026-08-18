#!/bin/bash
BASE_DIR=$1
retryCount=0
amcClientFunc(){
    echo $(date +"%Y-%m-%d %H:%M:%S")"：starting connecting to AMC"
    exec 8<>/dev/udp/${ENV_AMC_USABLITY_ADDR}/${ENV_AMC_USABLITY_PORT}
    if [ $? -eq 0 ];then
        echo $(date +"%Y-%m-%d %H:%M:%S")"：connecting to AMC succeed"
        retryCount=0
        return 0
    else
        retryCount=$[${retryCount}+1]
        echo $(date +"%Y-%m-%d %H:%M:%S")"：connect to AMC failed ${retryCount} times , will retry in $[${ENV_RETRY_INTERVAL}*${retryCount}] .."
        sleep $[${ENV_RETRY_INTERVAL}*${retryCount}]
    fi
}
while true
do
    amcClientFunc > $ENV_LOG_PATH/amc_client.log 2>&1
    if [ $? -eq 0 ]; then
        break
    fi
done

reportFunc(){
    IP=${HOST_IP}
    normalModule=()
    abnormalModule=()
    checkList=$(ls $BASE_DIR/checkList|grep -vw checkList.txt)
    
    echo $(date +"%Y-%m-%d %H:%M:%S")"：check list exec shells: ${checkList}"
    echo $(date +"%Y-%m-%d %H:%M:%S")"：retry interval: ${ENV_RETRY_INTERVAL}"
    echo $(date +"%Y-%m-%d %H:%M:%S")"：max retry count: ${ENV_MAX_RETRY_COUNT}"
    for checkShell in ${checkList};
    do
        #retry
        count=0
        compoment=$(echo ${checkShell}|awk -F '.' '{print $1}'|awk -F '_' '{print $2}')
        while [[ ${count} < ${ENV_MAX_RETRY_COUNT} ]]
        do
            echo $(date +"%Y-%m-%d %H:%M:%S")"：start the num: ${count} time to check: ${checkShell}"
            if ${BASE_DIR}/checkList/${checkShell};then
                echo $(date +"%Y-%m-%d %H:%M:%S")"：service ${compoment} is normal, execute next shell check"
                eval normalModule[${#normalModule[*]}]=${compoment}
                break
            else
                echo $(date +"%Y-%m-%d %H:%M:%S")"：service ${compoment} is abnormal, retry the num: ${count} time"
                if [[ $[${count}+1] > ${ENV_MAX_RETRY_COUNT} ]];then
                    eval abnormalModule[${#abnormalModule[*]}]=${compoment}
                    break
                fi
            fi
            count=$[${count}+1]
            sleep ${ENV_RETRY_INTERVAL}
        done
    done
    #send msg once
    echo $(date +"%Y-%m-%d %H:%M:%S")"：abnormalModule len: ${#abnormalModule[*]}"
    if [[ ${#abnormalModule[*]} > 0 ]]; then
        message="[$(date +'%Y-%m-%d %H:%M:%S')] running normally module: ${normalModule[*]}, running abnormally module: ${abnormalModule[*]}"
        echo ${message}
        echo "<?xml version=\"1.0\" encoding=\"GBK\" ?><APPROOT type=\"BAMC_USABILITY_REPORT\" ver=\"QZ\" from =\"F-PAAS\" to=\"F-AMC\"  mode=\"asy\"><PUBLIC><APPSNAME>${ENV_APP_NAME}</APPSNAME><TYPE>02</TYPE><IP>${IP}</IP><SUBAPPSIMPLE>${ENV_AMC_SUBAPP_NAME}</SUBAPPSIMPLE></PUBLIC><PRIVATE><ROWSET><ROW><OCCUREDAPPSNAME>${ENV_APP_NAME}</OCCUREDAPPSNAME><OCCUREDIP>${IP}</OCCUREDIP><MODULECODE>${ENV_AMC_MODULECODE}</MODULECODE><SUBMODULECODE>${ENV_AMC_MODULESUBCODE}</SUBMODULECODE><STATUS>2</STATUS><MSG>${message}</MSG></ROW></ROWSET></PRIVATE></APPROOT>" 1>&8
    else
        message="[$(date +'%Y-%m-%d %H:%M:%S')] all module running normally: ${normalModule[*]}"
        echo ${message}
        echo "<?xml version=\"1.0\" encoding=\"GBK\" ?><APPROOT type=\"BAMC_USABILITY_REPORT\" ver=\"QZ\" from =\"F-PAAS\" to=\"F-AMC\"  mode=\"asy\"><PUBLIC><APPSNAME>${ENV_APP_NAME}</APPSNAME><TYPE>02</TYPE><IP>${IP}</IP><SUBAPPSIMPLE>${ENV_AMC_SUBAPP_NAME}</SUBAPPSIMPLE></PUBLIC><PRIVATE><ROWSET><ROW><OCCUREDAPPSNAME>${ENV_APP_NAME}</OCCUREDAPPSNAME><OCCUREDIP>${IP}</OCCUREDIP><MODULECODE>${ENV_AMC_MODULECODE}</MODULECODE><SUBMODULECODE>${ENV_AMC_MODULESUBCODE}</SUBMODULECODE><STATUS>0</STATUS><MSG>${message}</MSG></ROW></ROWSET></PRIVATE></APPROOT>" 1>&8
    fi
}

amcFunc(){
    amc_shadow_pid=`ps -ef|grep amc_shadow.sh|sed '/grep/d'|awk '{print $2}'`
	if [ -z "$amc_shadow_pid" ]; then
		echo amc_shadow is down.
        nohup $BASE_DIR/amc_shadow.sh $BASE_DIR 1>$ENV_LOG_PATH/amc_shadow.log 2>&1 &
	else
		echo amc_shadow is up.
	fi
#start run checks
	reportFunc
	echo $(date +"%Y-%m-%d %H:%M:%S")":wait for the next check loop..."
	sleep 300
}

while true
do
	amcFunc > $ENV_LOG_PATH/amc.log
done