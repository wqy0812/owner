#!/bin/bash

##### 目录结构
# /.../ansible/
#  |--hosts  执行清单
#  |--tasks.log 日志文件
#  |--task/ playbook 执行文件（=openfuyao）   
#  |  |--group_vars/
#  |     |--all  ansible 配置文件
#  |  |--roles/  ansible 角色
#  |     |--xxx/ ansible 角色文件
#  |  |--run.sh ansible执行脚本（对外暴露）
#  |  |--ansible_run.sh ansible执行脚本 （通用）
#####

##### 接口说明文档
#  {                                                               
#    #集群名称                                                         
#    "clusterId": "$cluster_id",                                   
#    #任务编号                                                         
#    "taskId": "$taskId",                                          
#    #任务状态 finished/成功，failed/失败,partial_failed/部分成功
#    "status": "$status",                                          
#    #回调函数地址                                                       
#    "callbackUrl": "$url",                                        
#    #回调函数鉴权信息                                                     
#    "invoker": "$token",                                          
#    #任务详细描述                                                       
#    "message": "$message",                                        
#    #任务回传字段                                                       
#    "detail":{                                                    
#    # 动作: build/搭建, destroy/下线, addnodes/纳管, delnodes/退库          
#    "action": "$action",                                          
#    # 集群角色: manager/管理集群, work/业务集群                               
#    "clusterRole": "$cluster_role",                               
#    # 纳管成功节点信息 ["xxx.xxx.xxx.xxx","xxx.xxx.xxx.xxx"]              
#    "successNodeIPs": [$SUCCESS_IPS],
#    # 纳管失败节点信息 ["xxx.xxx.xxx.xxx","xxx.xxx.xxx.xxx"]              
#    "failedNodeIPs": [$FAILED_IPS]
#   }
#####

TASK_DIR=$(dirname "$(readlink -f "$0")")
HOST_FILE="${TASK_DIR}/../hosts"
LOG_FILE="${TASK_DIR}/../tasks.log"
ALL_FILE="${TASK_DIR}/group_vars/all"
declare -A MEMBERS
declare -A SUCCESS_IPS
declare -A FAILED_IPS
declare -a SUCCESS_NODES
declare -a FAILED_NODES
ACTION_MESSAGE="..."
ANSIBLE_BIN=/usr/local/bin

# 日志函数
log() {
  local log_level=$1
  local message="[$ACTION_MESSAGE]:$2"
  local timestamp=$(date "+%Y-%m-%d %H:%M:%S")
  echo -e "[${timestamp}] [${log_level}] ${message}" | tee -a ${LOG_FILE}
}

# 回调函数
call_back() {
  local url=$1
  local json_body=$2
  local head=$3
  log "INFO" "开始回调接口: ${url}"
  log "INFO" "回调参数: ${json_body}"
  # 最大重试次数
  MAX_RETRIES=3
  # 初始重试计数器
  retry_count=0
  # 请求成功标志
  success=false
  if [ -z "$head" ]; then
      head="Accept: application/json"
  fi
  while [ $retry_count -lt $MAX_RETRIES ] && [ "$success" = false ]; do
      # 发送POST请求
      response=$(curl -s -X POST \
          -H "Content-Type: application/json" \
          -H "$head" \
          -d "$json_body" \
          "$url")

      # 检查curl命令是否成功执行（$?获取上一条命令的退出状态）
      if [ $? -eq 0 ]; then
          success=true
          # 输出响应结果
          log "INFO" "API请求成功: $response"
      else
          retry_count=$((retry_count + 1))
          if [ $retry_count -lt $MAX_RETRIES ]; then
              log "WARNING" "API请求失败，准备第${retry_count}次重试..."
              sleep 1  # 每次重试前等待1秒
          else
              log "ERROR" "API请求失败，已达到最大重试次数${MAX_RETRIES}"
          fi
      fi
  done

  # 如果最终仍然失败
  if [ "$success" = false ]; then
      log "ERROR" "API请求最终失败"
      return 1
  fi

  return 0
}

# 退出并回调函数
clean_up_sensitive_files() {
  log "INFO" "开始清理host文件: ${HOST_FILE}"
  sed -i -E "s/([[:space:]]ansible_ssh_pass=['\"][^'\"]*['\"])([[:space:]]|$)//g" ${HOST_FILE}
  log "INFO" "开始清理日志文件: ${LOG_FILE}"
  sed -i -E 's/"invoker":.*/"invoker": "*"/g' ${LOG_FILE}
  log "INFO" "开始清理all文件: ${ALL_FILE}"
  sed -i -E 's/ENV_DOCKER_SECRET_PASSWORD:.*/ENV_DOCKER_SECRET_PASSWORD: "*"/g' ${ALL_FILE}
  sed -i -E 's/callbackToken:.*/callbackToken: "*"/g' ${ALL_FILE}
  sed -i -E 's/ENV_CHART_PULL_PASSWORD:.*/ENV_CHART_PULL_PASSWORD: "*"/g' ${ALL_FILE}
}

get_val() {
  local key=$1
  local value=$(grep "^${key}:" "$ALL_FILE" | awk '{print $2}')
  echo "$value"
}

pre_check() {
  if [ -z "$playbook" ]; then
    log "ERROR" "未指定playbook文件"
    return 1
  fi
  if [ -z "$action" ]; then
    log "ERROR" "未指定action"
    return 1
  fi
  if [ -z "$cluster_role" ]; then
    log "ERROR" "未指定cluster_role"
    return 1
  fi
  if [ "$cluster_role" == "manager" ] && [ "$action" == "build" ];then
    tags="init,image_plugin,rcv,ins"
    ACTION_MESSAGE="管理集群搭建"
  elif [ "$cluster_role" == "manager" ] && [ "$action" == "destroy" ];then
    tags="rcv"
    ACTION_MESSAGE="管理集群销毁"
  elif [ "$cluster_role" == "work" ] && [ "$action" == "build" ];then
    tags="init,image_plugin,rcv,ins"
    ACTION_MESSAGE="业务集群搭建"
  elif [ "$cluster_role" == "work" ] && [ "$action" == "destroy" ];then
    tags="rcv"
    ACTION_MESSAGE="业务集群销毁"
  elif [ "$cluster_role" == "work" ] && [ "$action" == "addnodes" ];then
    tags="rcv,ins,image_plugin"
    ACTION_MESSAGE="宿主机纳管"
  elif [ "$cluster_role" == "work" ] && [ "$action" == "delnodes" ];then
    tags="rcv"
    ACTION_MESSAGE="宿主机退库"
  else
    log "ERROR" "目前框架暂不支持该操作"
    return 1
  fi
}

# 解析nodes文件
set_nodes_result() {
  # 1. 获取成员信息
  get_host_members

  # 2. 将 MEMBERS 中的字符串按空格拆分，存入一个临时哈希表
  declare -a INTERSECTION

  declare -A target_map
  for ip in ${MEMBERS[work_cluster_k8snode]}; do
      target_map[$ip]=1
  done

  # 3. 遍历 SUCCESS_IPS，检查是否在临时哈希表中存在
  IFS=' ' read -r -a _success_ips <<< "$(echo "$SUCCESS_IPS" | tr -d '"' | tr ',' ' ')"
  for ip in "${_success_ips[@]}"; do
      echo "IP="$ip",TARGET="${target_map[$ip]}
      if [[ -n "${target_map[$ip]}" ]]; then
          SUCCESS_NODES+=("$ip")
      fi
  done

  # 4. 遍历 FAILED_IPS，检查是否在临时哈希表中存在
  IFS=' ' read -r -a _failed_ips <<< "$(echo "$FAILED_IPS" | tr -d '"' | tr ',' ' ')"
  for ip in "${_failed_ips[@]}"; do
      echo "IP="$ip",TARGET="${target_map[$ip]}
      if [[ -n "${target_map[$ip]}" ]]; then
          FAILED_NODES+=("$ip")
      fi
  done

  # 4. 输出结果
  echo "成功宿主机: ${SUCCESS_NODES[@]}"
  echo "失败宿主机: ${FAILED_NODES[@]}"
}

# 解析host文件
get_host_members() {
  # 检查文件是否存在
  if [ ! -f "$HOST_FILE" ]; then
      echo "错误: 找不到文件 $HOST_FILE"
      exit 1
  fi

  current_group=""
  # 使用文件描述符读取，防止管道创建子shell导致变量丢失
  while IFS= read -r line || [ -n "$line" ]; do
      # 优化：使用 Bash 内置变量替换去除首尾空格，替代 sed
      trimmed_line="${line#"${line%%[![:space:]]*}"}"
      trimmed_line="${trimmed_line%"${trimmed_line##*[![:space:]]}"}"

      # 跳过空行和注释行
      if [[ -z "$trimmed_line" ]] || [[ "$trimmed_line" == \#* ]]; then
          continue
      fi

      # 检测是否为组名定义行 [group_name]
      if [[ "$trimmed_line" == \[*\] ]]; then
          # 提取组名：去掉方括号
          current_group="${trimmed_line//[}"
          current_group="${current_group//]}"
      else
          if [[ -n "$current_group" ]]; then
              # 优化：使用内置变量替换提取第一个字段，替代 awk
              ip="${trimmed_line%% *}" # 删除第一个空格后的所有内容

              # 追加 IP
              if [[ -n "${MEMBERS[$current_group]}" ]]; then
                  MEMBERS[$current_group]="${MEMBERS[$current_group]} $ip"
              else
                  MEMBERS[$current_group]="$ip"
              fi
          fi
      fi
  done < "$HOST_FILE"

  # 格式化输出结果
  echo "全局 MEMBERS 对象内容如下："
  echo "----------------------------------------"

  for group in "${!MEMBERS[@]}"; do
      result="["
      # 修正：正确访问数组元素 ${MEMBERS[$group]}
      # 不加引号，让 Bash 自动按空格分割字符串进行循环
      for ip in ${MEMBERS[$group]}; do
        result+="\"$ip\","
      done
      result="${result%,}]"
      echo "$group=$result"
  done

  echo "----------------------------------------"
}

# 主函数
run_ansible() {
    truncate -s 0 $LOG_FILE
    log "INFO" "开始执行ansible任务:$*"
    playbook=$1
    cluster_role=$2
    action=$3
    cluster_id=$(get_val "cluster_id")
    if ! pre_check; then
      log "ERROR" "参数验证失败"
      return 1
    fi
    cd ${TASK_DIR}
    log "INFO" "开始执行Ansible Playbook: ${playbook}"
    log "INFO" "主机清单文件: ${HOST_FILE}"
    # 执行Ansible Playbook并捕获输出和返回码
    log "INFO" "Ansible执行输出: ${ANSIBLE_BIN}/ansible-playbook -v -i '${HOST_FILE}'  -e 'cluster_role=${cluster_role}' '${playbook}' --tags=${tags} 2>&1 "
    local output=$(${ANSIBLE_BIN}/ansible-playbook -v -i "${HOST_FILE}" -e "cluster_role=${cluster_role}" "${playbook}" --tags=${tags} 2>&1 | tee /dev/tty)
    local ret_code="${PIPESTATUS[0]}"
    local stats=$(echo "$output" | grep -oP 'failed=\d+|unreachable=\d+|ok=\d+')
    local failed=$(echo "$stats" | grep -E 'failed=[1-9][0-9]*' | wc -l)
    local unreachable=$(echo "$stats" | grep -E 'unreachable=[1-9][0-9]*' | wc -l)
    local ok=$(echo "$stats" | grep -E 'ok=[1-9][0-9]*' | wc -l)
    # 记录完整输出
    log "INFO" "Ansible执行返回码:${ret_code}"
    log "INFO" "Ansible执行输出:\n${output}\n"

    # 初始化关联数组用于去重
    declare -A success__ips
    declare -A failed__ips

    # 处理文件内容，提取IP和状态
    while IFS= read -r line; do
        # 提取 IP 地址
        if [[ $line =~ ^([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+) ]]; then
            _ip=${BASH_REMATCH[1]}
            log "INFO" "IP信息输出:${_ip}"
            # 过滤 127.0.0.1
            if [[ "$_ip" == "127.0.0.1" ]]; then
                continue
            fi
            # 1. 优先判断是否包含 failed=非0 或 unreachable=非0
            if [[ $line =~ (failed|unreachable)=([1-9][0-9]*) ]]; then
                failed__ips["$_ip"]=1
            # 2. 其次判断是否包含 ok= (确认为执行过的节点)
            elif [[ $line =~ ok=([0-9]+) ]]; then
                success__ips["$_ip"]=1
            fi
        fi
    done < "$LOG_FILE"

    # 将数组键转换为带引号的IP列表
    SUCCESS_IPS=$(printf "\"%s\"\n" "${!success__ips[@]}" | sort | paste -sd,)
    FAILED_IPS=$(printf "\"%s\"\n" "${!failed__ips[@]}" | sort | paste -sd,)

    # 输出结果
    log "INFO" "成功的机器列表: [$SUCCESS_IPS]"
    log "INFO" "失败的机器列表: [$FAILED_IPS]"

    # 判断执行结果
    if [ "$ok" -gt 0 ] && [ "$failed" -eq 0 ] && [ "$unreachable" -eq 0 ];then
      log "INFO" "Ansible Playbook执行成功"
      return 0
    fi
    log "ERROR" "Ansible Playbook执行失败,返回码:${ret_code},成功：${ok},失败：${failed},不可达：${unreachable}"
    return 1
}