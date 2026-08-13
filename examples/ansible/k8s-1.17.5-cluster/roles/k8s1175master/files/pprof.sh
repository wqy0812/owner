date=`date +%Y%m%d` && mkdir -p /opt/pprof/$date && cd /opt/pprof/$date &&
ip=$(ip addr | awk '/^[0-9]+: / {}; /inet.*global/ {print gensub(/(.*)\/(.*)/, "\\1", "g", $2)}' | head -n 1) &&
wget http://127.0.0.1:10251/debug/pprof/heap && mv heap kube-scheduler.heap &&
wget http://127.0.0.1:10251/debug/pprof/profile && mv profile kube-scheduler.profile &&
wget http://127.0.0.1:10251/debug/pprof/goroutine?debug=1 && mv goroutine?debug=1 kube-scheduler.goroutine &&
wget http://$ip:8080/debug/pprof/heap && mv heap kube-apiserver.heap &&
wget http://$ip:8080/debug/pprof/profile && mv profile kube-apiserver.profile &&
wget http://$ip:8080/debug/pprof/goroutine?debug=1 && mv goroutine?debug=1 kube-apiserver.goroutine
