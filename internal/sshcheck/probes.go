package sshcheck

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type Probe struct {
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	Interpreter string `json:"interpreter,omitempty"`
	// Resolve an OCI reference's transport from the consuming host's existing
	// Docker policy. This never changes that policy or the selected registry.
	RegistryTransportFromDocker bool `json:"registryTransportFromDocker,omitempty"`
}
type ProbeResult struct {
	Status   string `json:"status"`
	Observed string `json:"observed"`
	Message  string `json:"message,omitempty"`
}

var probeName = regexp.MustCompile(`^[a-zA-Z0-9_./:@+-]+$`)

func quoteProbe(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func ProbeCommand(probe Probe) (string, error) {
	target := probe.Target
	if target == "" || len(target) > 4096 || (!probeName.MatchString(target) && probe.Kind != "tcp" && probe.Kind != "image_command" && probe.Kind != "http" && probe.Kind != "registry") {
		return "", fmt.Errorf("invalid read-only probe target")
	}
	switch probe.Kind {
	case "http", "registry":
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(target, "\r\n\x00") {
			return "", fmt.Errorf("probe requires an HTTP endpoint without embedded credentials")
		}
		interpreter := probe.Interpreter
		if interpreter == "" {
			interpreter = "python3"
		}
		if !probeName.MatchString(interpreter) {
			return "", fmt.Errorf("invalid probe interpreter")
		}
		code := "import urllib.request, urllib.error\nendpoint=" + strconv.Quote(target) + "\n"
		if probe.Kind == "registry" && probe.RegistryTransportFromDocker {
			code += "import json, subprocess, ipaddress, socket\n" +
				"try:\n policy=json.loads(subprocess.check_output(['docker','info','--format','{'*2+'json .RegistryConfig'+'}'*2],stderr=subprocess.DEVNULL,timeout=5).decode())\nexcept Exception:\n raise SystemExit('Cannot read Docker registry transport policy; no transport fallback attempted')\n" +
				"registry=" + strconv.Quote(u.Host) + "\nconfigured=policy.get('IndexConfigs',{}).get(registry,{})\ninsecure=configured.get('Secure') is False\n" +
				"if not insecure and 'Secure' not in configured:\n try:\n  addresses=[ipaddress.ip_address(item[4][0]) for item in socket.getaddrinfo(" + strconv.Quote(u.Hostname()) + ",None)]\n  networks=[ipaddress.ip_network(value) for value in policy.get('InsecureRegistryCIDRs',[])]\n  insecure=any(address in network for address in addresses for network in networks if address.version==network.version)\n except (ValueError,OSError):\n  raise SystemExit('Cannot resolve configured Docker registry transport policy')\n" +
				"if insecure:\n endpoint='http://'+registry+'/v2/'\nprint('Registry transport '+endpoint.split(':',1)[0]+' from existing Docker policy')\n"
		}
		code += "request=urllib.request.Request(endpoint,headers={'Range':'bytes=0-0'})\ntry:\n response=urllib.request.urlopen(request,timeout=8)\n response.read(1)\n print('HTTP '+str(response.status)+' reachable')\n response.close()\nexcept urllib.error.HTTPError as error:\n"
		if probe.Kind == "registry" {
			code += " if error.code==401: print('Registry endpoint reachable; authentication is checked separately against the selected image')\n else: raise SystemExit('HTTP '+str(error.code))\n"
		} else {
			code += " raise SystemExit('HTTP '+str(error.code))\n"
		}
		return quoteProbe(interpreter) + " -c " + quoteProbe(code), nil

	case "image_command":
		parts := strings.Split(target, "#")
		if len(parts) != 2 || !probeName.MatchString(parts[0]) || !regexp.MustCompile(`@sha256:[a-f0-9]{64}$`).MatchString(parts[0]) || !regexp.MustCompile(`^[a-zA-Z0-9_.+-]+$`).MatchString(parts[1]) {
			return "", fmt.Errorf("image tool probe requires image@sha256:digest#command")
		}
		// Inspect an existing exact image, then use only its local image ID. A
		// missing image cannot trigger a registry pull or source substitution.
		// Parse Docker's JSON instead of embedding its Go-template braces in a
		// command which is also consumed by Ansible's Jinja templating engine.
		decodeID := "import json,sys; images=json.load(sys.stdin); assert len(images)==1; print(images[0]['Id'])"
		return "image=$(docker image inspect " + quoteProbe(parts[0]) + " | python3 -c " + quoteProbe(decodeID) + ") || exit $?; case \"$image\" in sha256:*) ;; *) exit 1;; esac; docker run --rm --read-only --network none --entrypoint /bin/sh \"$image\" -c " + quoteProbe("command -v "+quoteProbe(parts[1])), nil
	case "command":
		return "command -v " + quoteProbe(target), nil
	case "path_present", "path_absent":
		if !strings.HasPrefix(target, "/") || path.Clean(target) != target || target == "/" {
			return "", fmt.Errorf("probe requires a normalized absolute path")
		}
		if probe.Kind == "path_absent" {
			return "if test -e " + quoteProbe(target) + " || test -L " + quoteProbe(target) + "; then printf 'present'; exit 1; else printf 'absent'; fi", nil
		}
		return "if test -e " + quoteProbe(target) + "; then printf 'present'; else printf 'absent'; exit 1; fi", nil
	case "service_inactive":
		return "systemctl is-active " + quoteProbe(target) + "; code=$?; test \"$code\" -eq 3 -o \"$code\" -eq 4", nil
	case "network_rules_absent":
		return "rules=$(iptables-save) || exit $?; if printf '%s' \"$rules\" | grep -F -q -- " + quoteProbe(target) + "; then printf 'matching rules remain'; exit 1; else printf 'no matching rules'; fi", nil
	case "tcp":
		host, port, e := net.SplitHostPort(target)
		if e != nil {
			return "", e
		}
		if !probeName.MatchString(host) {
			return "", fmt.Errorf("invalid probe host")
		}
		number, e := strconv.Atoi(port)
		if e != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("invalid probe port")
		}
		code := "import socket; s=socket.create_connection((" + strconv.Quote(host) + "," + port + "),5); s.close(); print('reachable')"
		return "python3 -c " + quoteProbe(code), nil
	default:
		return "", fmt.Errorf("unsupported read-only probe kind")
	}
}
func (c *Checker) Probe(ctx context.Context, request Request, probe Probe) (ProbeResult, error) {
	command, err := ProbeCommand(probe)
	if err != nil {
		return ProbeResult{}, err
	}
	output, err := c.run(ctx, request, command)
	result := ProbeResult{Status: "passed", Observed: strings.TrimSpace(string(output))}
	if err != nil {
		result.Status = "failed"
		result.Message = err.Error()
	}
	return result, err
}

// Bound output independently from the SSH command result.
type probeBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *probeBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(value)
	remaining := 4096 - len(b.data)
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		b.data = append(b.data, value...)
	}
	return n, nil
}
