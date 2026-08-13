#!/bin/bash
# 生成密钥
MASTER_KEY="$(openssl rand -base64  32)"

# 加密用户名和密码
USERNAME="$1"
PASSWORD="$2"

ENCRYPTED_USERNAME="$(echo -n "${USERNAME}" | openssl enc -e -aes-256-cbc -a -salt -pbkdf2 -iter 100000 -md sha256 -pass pass:"${MASTER_KEY}")"
ENCRYPTED_PASSWORD="$(echo -n "${PASSWORD}" | openssl enc -e -aes-256-cbc -a -salt -pbkdf2 -iter 100000 -md sha256 -pass pass:"${MASTER_KEY}")"

# 创建存储目录
sudo mkdir -p /etc/icbc/encrypted-credentials
sudo chmod 500 /etc/icbc/encrypted-credentials

# 保存加密凭证
sudo tee /etc/icbc/encrypted-credentials/fyhub_encrypted.conf > /dev/null <<EOF
ENC_USERNAME=${ENCRYPTED_USERNAME}
ENC_PASSWORD=${ENCRYPTED_PASSWORD}
EOF

# 保存密钥
echo "${MASTER_KEY}" | sudo tee /etc/icbc/encrypted-credentials/.aes_master.key > /dev/null

# 设置权限
sudo chmod 400 /etc/icbc/encrypted-credentials/.aes_master.key
sudo chmod 400 /etc/icbc/encrypted-credentials/fyhub_encrypted.conf