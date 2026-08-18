#!/bin/bash
date=`date +%Y%m%d` && mkdir -p /opt/pprof/$date && cd /opt/pprof/$date &&
ip=$(ip addr | awk '/^[0-9]+: / {}; /inet.*global/ {print gensub(/(.*)\/(.*)/, "\\1", "g", $2)}' | head -n 1) &&
wget http://$ip:10255/debug/pprof/heap && mv heap kubelet.heap &&
wget http://$ip:10255/debug/pprof/profile && mv profile kubelet.profile &&
wget http://$ip:10255/debug/pprof/goroutine?debug=1 && mv goroutine?debug=1 kubelet.goroutine                                                                                          