package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

type RunDiagnostic struct {
	StepID    string `json:"stepId,omitempty"`
	LogID     int64  `json:"logId,omitempty"`
	Component string `json:"component,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Task      string `json:"task,omitempty"`
	Host      string `json:"host,omitempty"`
	Message   string `json:"message"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Source    string `json:"source"`
	Truncated bool   `json:"truncated,omitempty"`
}

type RunDiagnostics struct {
	RunID      string           `json:"runId"`
	Status     domain.RunStatus `json:"status"`
	CapturedAt time.Time        `json:"capturedAt"`
	LastLogID  int64            `json:"lastLogId"`
	LogCount   int64            `json:"logCount"`
	Items      []RunDiagnostic  `json:"items"`
	Omitted    int              `json:"omitted,omitempty"`
}

var diagnosticANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var diagnosticFatal = regexp.MustCompile(`(?is)^(?:fatal|unreachable):\s*\[([^\]]+)\]:\s*(?:FAILED!|UNREACHABLE!)?\s*(?:=>)?\s*(.*)$`)
var diagnosticTask = regexp.MustCompile(`^TASK \[(.+)\]\s*\**$`)
var diagnosticException = regexp.MustCompile(`(?i)([a-zA-Z]+Error:|[a-zA-Z]+Exception:|connection.*refused|permission denied|no such file|timed out|timeout|failed to|unable to|cannot |assertion|错误|失败|超时)`)

// Keep diagnostic responses bounded; the bundle always contains the full logs.
func diagnosticExcerpt(s string) (string, bool) {
	s = strings.TrimSpace(diagnosticANSI.ReplaceAllString(s, ""))
	r := []rune(s)
	if len(r) > 16000 {
		return "…\n" + string(r[len(r)-16000:]), true
	}
	return s, false
}
func diagnosticString(v any) string { s, _ := v.(string); return s }
func diagnosticOutput(v map[string]any, key string) string {
	if s := diagnosticString(v[key]); s != "" {
		return s
	}
	if lines, ok := v[key+"_lines"].([]any); ok {
		out := []string{}
		for _, line := range lines {
			if s, ok := line.(string); ok {
				out = append(out, s)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}
func diagnosticReason(result map[string]any) string {
	msg := diagnosticString(result["msg"])
	stdout, stderr := diagnosticOutput(result, "stdout"), diagnosticOutput(result, "stderr")
	// A traceback often lives in stdout while stderr only says SSH was closed.
	for _, output := range []string{diagnosticString(result["exception"]), stdout, stderr} {
		lines := strings.Split(output, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if diagnosticException.MatchString(line) && !strings.HasPrefix(line, "File ") && !strings.HasPrefix(line, "raise ") {
				return line
			}
		}
	}
	if msg != "" && !strings.EqualFold(msg, "non-zero return code") && !strings.EqualFold(msg, "MODULE FAILURE") {
		return msg
	}
	for _, output := range []string{stderr, stdout} {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" && !(strings.HasPrefix(line, "Shared connection to ") && strings.HasSuffix(line, "closed.")) {
				return line
			}
		}
	}
	if msg != "" {
		return msg
	}
	return "任务失败，未返回具体错误说明"
}

type diagnosticCollector struct {
	structured, text []RunDiagnostic
	omitted          int
	legacyPending    map[string]domain.RunLog
	legacyTasks      map[string]string
}

func (c *diagnosticCollector) add(log domain.RunLog) {
	line := strings.TrimSpace(diagnosticANSI.ReplaceAllString(log.Message, ""))
	key := log.StepID + "\x00" + log.Stream
	if log.Stream != "event" {
		// Older Ansible stdout callbacks can emit a failure JSON object over
		// several persisted rows. Keep its first log ID as the source location.
		if previous, ok := c.legacyPending[key]; ok {
			if diagnosticTask.MatchString(line) || strings.HasPrefix(line, "PLAY ") || diagnosticFatal.MatchString(line) {
				c.addComplete(previous, c.legacyTasks[key])
				delete(c.legacyPending, key)
			} else {
				previous.Message += "\n" + line
				match := diagnosticFatal.FindStringSubmatch(previous.Message)
				if (match != nil && json.Valid([]byte(match[2]))) || len(previous.Message) > 1024*1024 {
					c.addComplete(previous, c.legacyTasks[key])
					delete(c.legacyPending, key)
				} else {
					c.legacyPending[key] = previous
				}
				return
			}
		}
		if match := diagnosticTask.FindStringSubmatch(line); match != nil {
			if c.legacyTasks == nil {
				c.legacyTasks = map[string]string{}
			}
			c.legacyTasks[key] = match[1]
			return
		}
		if match := diagnosticFatal.FindStringSubmatch(line); match != nil && strings.HasPrefix(match[2], "{") && !json.Valid([]byte(match[2])) && len(line) <= 1024*1024 {
			if c.legacyPending == nil {
				c.legacyPending = map[string]domain.RunLog{}
			}
			log.Message = line
			c.legacyPending[key] = log
			return
		}
	}
	c.addComplete(log, c.legacyTasks[key])
}

func (c *diagnosticCollector) addComplete(log domain.RunLog, task string) {
	line := strings.TrimSpace(diagnosticANSI.ReplaceAllString(log.Message, ""))
	d := RunDiagnostic{LogID: log.ID, StepID: log.StepID, Source: "text", Task: task}
	var result map[string]any
	if log.Stream == "event" {
		var event struct {
			Kind, StepID, Host, Task, Status string
			Result                           map[string]any
		}
		if json.Unmarshal([]byte(line), &event) != nil || event.Kind != "result" || (event.Status != "failed" && event.Status != "unreachable") {
			return
		}
		d.Source = "event"
		d.Host = event.Host
		d.Task = event.Task
		result = event.Result
	} else if m := diagnosticFatal.FindStringSubmatch(line); m != nil {
		d.Host = m[1]
		if json.Unmarshal([]byte(m[2]), &result) != nil {
			d.Message = m[2]
			if strings.TrimSpace(d.Message) == "{" {
				d.Message = "任务失败，旧文本日志未记录完整错误结果"
			}
		}
	} else if strings.HasPrefix(line, "ERROR!") || strings.HasPrefix(line, "[ERROR]:") || (log.Stream == "system" && diagnosticException.MatchString(line)) {
		d.Message = line
	} else {
		return
	}
	if hidden, _ := result["_ansible_no_log"].(bool); hidden || result["censored"] != nil {
		d.Task = "受保护任务"
		d.Message = "任务失败，详细输出受 no_log 保护"
		d.Raw = "输出已隐藏"
	} else {
		if result != nil {
			d.Message = diagnosticReason(result)
			var cut bool
			d.Stdout, d.Truncated = diagnosticExcerpt(diagnosticOutput(result, "stdout"))
			d.Stderr, cut = diagnosticExcerpt(diagnosticOutput(result, "stderr"))
			d.Truncated = d.Truncated || cut
			if code, ok := result["rc"].(float64); ok {
				n := int(code)
				d.ExitCode = &n
			}
		}
		var cut bool
		d.Raw, cut = diagnosticExcerpt(line)
		d.Truncated = d.Truncated || cut
	}
	var cut bool
	d.Message, cut = diagnosticExcerpt(d.Message)
	d.Truncated = d.Truncated || cut
	if d.Message == "" {
		d.Message = "任务失败，未返回具体错误说明"
	}
	target := &c.text
	if d.Source == "event" {
		target = &c.structured
	}
	if len(*target) >= 200 {
		c.omitted++
		return
	}
	*target = append(*target, d)
}
func (c *diagnosticCollector) finish(run domain.Run) []RunDiagnostic {
	for key, log := range c.legacyPending {
		c.addComplete(log, c.legacyTasks[key])
		delete(c.legacyPending, key)
	}
	items := []RunDiagnostic{}
	seen := map[string]bool{}
	structuredSeen := map[string]bool{}
	metadata := map[string]map[string]any{}
	if locked, ok := run.InputSnapshot["steps"].([]any); ok {
		for _, v := range locked {
			if m, ok := v.(map[string]any); ok {
				metadata[diagnosticString(m["nodeId"])] = m
				metadata[diagnosticString(m["id"])] = m
			}
		}
	}
	failed := map[string]bool{}
	steps := map[string]domain.RunStep{}
	for _, step := range run.Steps {
		steps[step.ID] = step
		if step.Status == domain.RunFailed {
			failed[step.ID] = true
		}
	}
	for _, d := range append(c.structured, c.text...) {
		baseKey := d.StepID + "\x00" + d.Host + "\x00" + d.Message
		if d.Source == "text" && structuredSeen[baseKey] {
			continue
		}
		key := baseKey + "\x00" + d.Task
		if seen[key] {
			continue
		}
		seen[key] = true
		if d.Source == "event" {
			structuredSeen[baseKey] = true
		}
		if step, ok := steps[d.StepID]; ok {
			d.Component = step.Name
			if m := metadata[step.NodeID]; m != nil {
				if name := diagnosticString(m["componentName"]); name != "" {
					d.Component = name
				}
				d.Phase = diagnosticString(m["phase"])
			}
		}
		items = append(items, d)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := failed[items[i].StepID], failed[items[j].StepID]
		if a != b {
			return a
		}
		if items[i].Source != items[j].Source {
			return items[i].Source == "event"
		}
		return items[i].LogID < items[j].LogID
	})
	if len(items) == 0 && (run.Status == domain.RunFailed || run.Status == domain.RunInterrupted || run.Status == domain.RunCancelled) {
		d := RunDiagnostic{Message: run.Error, Source: "run"}
		for _, step := range run.Steps {
			if step.Status == domain.RunFailed {
				d.StepID = step.ID
				d.Component = step.Name
				if d.Message == "" {
					d.Message = step.Summary
				}
				break
			}
		}
		if d.Message == "" {
			if run.Status == domain.RunInterrupted {
				d.Message = "运行被中断，未记录更具体的原因"
			} else if run.Status == domain.RunCancelled {
				d.Message = "运行已取消"
			} else {
				d.Message = "运行失败，未记录具体错误；请下载完整日志包核对"
			}
		}
		d.Message, d.Truncated = diagnosticExcerpt(d.Message)
		items = append(items, d)
	}
	return items
}

func (s *ExecutionService) RunDiagnostics(ctx context.Context, user domain.User, id string) (RunDiagnostics, error) {
	var c diagnosticCollector
	snapshot, err := s.readRunLogs(ctx, user, id, func(l domain.RunLog) error { c.add(l); return nil })
	if err != nil {
		return RunDiagnostics{}, fmt.Errorf("读取运行诊断失败: %w", err)
	}
	return RunDiagnostics{RunID: id, Status: snapshot.Run.Status, CapturedAt: snapshot.CapturedAt, LastLogID: snapshot.LastLogID, LogCount: snapshot.LogCount, Items: c.finish(snapshot.Run), Omitted: c.omitted}, nil
}
