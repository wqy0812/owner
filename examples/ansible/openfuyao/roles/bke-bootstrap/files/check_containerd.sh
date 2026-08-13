#!/bin/bash
systemctl is-active containerd >/dev/null 2>&1
if [ $? -ne 0 ];then
	echo containerd service is not active
	exit 1
fi
containerd --version >/dev/null 2>&1
if [ $? -ne 0 ];then
	echo containerd sock is down, please restart containerd
	exit 1
fi
exit 0
