package ansible

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	recapField = regexp.MustCompile(`([a-z_]+)=(\d+)`)
	ansiCode   = regexp.MustCompile(`\x1b\[[0-9;]*[[:alpha:]]`)
)

type recapParser struct {
	mu    sync.Mutex
	hosts map[string]HostRecap
}

func newRecapParser() *recapParser {
	return &recapParser{hosts: make(map[string]HostRecap)}
}

func (p *recapParser) Add(line string) {
	line = ansiCode.ReplaceAllString(line, "")
	colon := strings.Index(line, ":")
	if colon <= 0 {
		return
	}
	host := strings.TrimSpace(line[:colon])
	fields := recapField.FindAllStringSubmatch(line[colon+1:], -1)
	if host == "" || len(fields) == 0 {
		return
	}
	values := make(map[string]int, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field[2])
		if err == nil {
			values[field[1]] = value
		}
	}
	if _, ok := values["ok"]; !ok {
		return
	}
	p.mu.Lock()
	p.hosts[host] = HostRecap{
		OK:          values["ok"],
		Changed:     values["changed"],
		Unreachable: values["unreachable"],
		Failed:      values["failed"],
		Skipped:     values["skipped"],
		Rescued:     values["rescued"],
		Ignored:     values["ignored"],
	}
	p.mu.Unlock()
}

func (p *recapParser) Snapshot() map[string]HostRecap {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make(map[string]HostRecap, len(p.hosts))
	for host, recap := range p.hosts {
		result[host] = recap
	}
	return result
}
