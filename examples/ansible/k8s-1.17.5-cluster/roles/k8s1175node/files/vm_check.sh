#!/bin/bash
checkFlag=0
# Check user and group of the sysop directory
sysopUser=`ls -l /home | awk -F ' ' '{if($NF=="sysop") print $0}' | awk '{print $3}'`
sysopGrp=`cat /etc/passwd | grep -P "^$sysopUser:" | awk -F ':' '{print $4}'`
if [[ $sysopGrp = "0" ]]
then
        echo -e "sysop: OK"
else
        echo -e "sysop: abnormal"
        let checkFlag=$checkFlag+1
fi

# Check dockerlv is btrfs or not
if  [ `grep -c "SUSE" /etc/os-release` -ne '0' ]; then
        dockerlv=`cat /etc/fstab | grep -P "/var/lib/docker" | awk '{print $3}'`
        if [[ $dockerlv = "btrfs" ]]
        then
                echo -e "dockerlv: OK"
        else
                echo -e "dockerlv: abnormal"
                let checkFlag=$checkFlag+1
        fi
else
        exit 0
fi

# IP address format validation function for hostname check
function IPAddressValidation()
{
        local ipExist=`echo $1 | grep -Po "^((0{0,2}\d|0?[1-9]\d|1\d{2}|2[0-4]\d|25[0-5])\.){3}(0{0,2}\d|0?[1-9]\d|1\d{2}|2[0-4]\d|25[0-5])$" | wc -l`
        if [ $ipExist -gt 0 ];then
                return 1
        else
                return 0
        fi
}

# Check hostname status
if ! IPAddressValidation `hostname -i`
then
        echo -e "hostname: OK"
else
        echo -e "hostname: abnormal"
        let checkFlag=$checkFlag+1
fi

# Check systeminfo
if [ -e /tempfile/system.info ]; then
        if [ `grep -c "DOCKER" /tempfile/system.info` -ne '0' ];then
                echo -e "systeminfo: ok"
        else
                echo -e "systeminfo: abnormal"
                let checkFlag=$checkFlag+1
        fi
else
        echo -e "systeminfo: no exit "
        let checkFlag=$checkFlag+1
fi

# Check result
if [ $checkFlag -gt 0 ]; then
        echo "there are $checkFlag not ok"
        exit 1
else
        echo "all check ok"
        exit 0
fi 


wo 