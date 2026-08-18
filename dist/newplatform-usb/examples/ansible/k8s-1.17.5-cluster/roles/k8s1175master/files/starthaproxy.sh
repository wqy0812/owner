#!/bin/bash
/root/haproxy/haproxy -f /root/haproxy/conf/haproxy_k8smaster.cfg 1>>/root/haproxy/haproxy.log 2>&1
