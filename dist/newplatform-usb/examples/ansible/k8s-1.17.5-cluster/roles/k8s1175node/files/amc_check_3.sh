#!/bin/bash
systemctl is-active docker >/dev/null 2>&1
if [ $? -ne 0 ];then
	echo docker service is not active
	exit 1
fi
docker version >/dev/null 2>&1
if [ $? -ne 0 ];then
	echo docker sock is down, please restart docker
	exit 1
fi
exit 0
