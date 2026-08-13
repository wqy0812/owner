#!/bin/bash
set -x && 
cd /root/ssl && \
./cfssl gencert -ca=ca.pem \
  -ca-key=ca-key.pem \
  -config=ca-config.json \
  -profile=kubernetes docker-csr.json | ./cfssljson -bare docker \
&& mkdir -p /root/.docker/ \
&& mv docker-key.pem /root/.docker/key.pem \
&& mv docker.pem /root/.docker/cert.pem \
&& mv ca.pem /root/.docker/ca.pem