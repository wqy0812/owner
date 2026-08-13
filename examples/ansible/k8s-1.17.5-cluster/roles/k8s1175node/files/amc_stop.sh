#!/bin/bash
kill -9 `ps -ef|grep -e amc_monitor -e amc_shadow -e amc_event_monitor|sed '/grep/d'|awk '{print $2}'`
exec 8>&-
