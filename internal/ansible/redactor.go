package ansible

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|token|secret|credential|private[_-]?key|encryption[_-]?key|access[_-]?key|ansible_ssh_pass)`)

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)("(?:password|passwd|token|secret|credential|private[_-]?key|encryption[_-]?key|access[_-]?key|ansible_ssh_pass)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)((?:password|passwd|token|secret|credential|private[_-]?key|encryption[_-]?key|access[_-]?key|ansible_ssh_pass)\s*:\s*)(?:'[^']*'|"[^"]*"|[^\s,}\]]+)`),
	regexp.MustCompile(`(?i)((?:password|passwd|token|secret|credential|private[_-]?key|encryption[_-]?key|access[_-]?key|ansible_ssh_pass)\s*=\s*)(?:'[^']*'|"[^"]*"|[^\s]+)`),
	regexp.MustCompile(`(?i)(authorization\s*:\s*(?:bearer|basic)\s+)[^\s]+`),
	regexp.MustCompile(`(?i)(https?://[^:/\s]+:)[^@/\s]+@`),
}

type Redactor struct {
	mu           sync.Mutex
	secrets      []string
	inPrivateKey bool
}

func NewRedactor(explicit []string, variables map[string]any, inventory []byte) *Redactor {
	values := append([]string(nil), explicit...)
	collectSensitiveValues(variables, false, &values)
	values = append(values, sensitiveInventoryValues(string(inventory))...)
	candidates := append([]string(nil), values...)
	unique := make(map[string]struct{}, len(values))
	values = make([]string, 0, len(candidates))
	for _, value := range candidates {
		parts := append([]string{value}, strings.FieldsFunc(value, func(r rune) bool { return r == '\r' || r == '\n' })...)
		for _, part := range parts {
			if part == "" || strings.Contains(part, "{{") || strings.Contains(part, "${") || part == "*" || part == "[REDACTED]" {
				continue
			}
			if _, exists := unique[part]; exists {
				continue
			}
			unique[part] = struct{}{}
			values = append(values, part)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return &Redactor{secrets: values}
}

func collectSensitiveValues(value any, inherited bool, dst *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			collectSensitiveValues(nested, inherited || sensitiveKey.MatchString(key), dst)
		}
	case map[string]string:
		for key, nested := range typed {
			collectSensitiveValues(nested, inherited || sensitiveKey.MatchString(key), dst)
		}
	case []any:
		for _, nested := range typed {
			collectSensitiveValues(nested, inherited, dst)
		}
	case []string:
		if inherited {
			*dst = append(*dst, typed...)
		}
	case string:
		if inherited {
			*dst = append(*dst, typed)
		}
	case fmt.Stringer:
		if inherited {
			*dst = append(*dst, typed.String())
		}
	default:
		if inherited && typed != nil {
			*dst = append(*dst, fmt.Sprint(typed))
		}
	}
}

func sensitiveInventoryValues(inventory string) []string {
	re := regexp.MustCompile(`(?i)(?:password|passwd|token|secret|private[_-]?key|encryption[_-]?key|access[_-]?key|ansible_ssh_pass)\s*(?:=|:)\s*["']?([^\s"']+)`)
	matches := re.FindAllStringSubmatch(inventory, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			values = append(values, match[1])
		}
	}
	return values
}

func (r *Redactor) Redact(line string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	upper := strings.ToUpper(line)
	if strings.Contains(upper, "-----BEGIN ") && strings.Contains(upper, "PRIVATE KEY-----") {
		r.inPrivateKey = true
		return "[REDACTED PRIVATE KEY]"
	}
	if r.inPrivateKey {
		if strings.Contains(upper, "-----END ") && strings.Contains(upper, "PRIVATE KEY-----") {
			r.inPrivateKey = false
		}
		return "[REDACTED PRIVATE KEY]"
	}

	redacted := line
	for _, secret := range r.secrets {
		redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
	}
	redacted = redactionPatterns[0].ReplaceAllString(redacted, `${1}[REDACTED]${2}`)
	redacted = redactionPatterns[1].ReplaceAllString(redacted, `${1}[REDACTED]`)
	redacted = redactionPatterns[2].ReplaceAllString(redacted, `${1}[REDACTED]`)
	redacted = redactionPatterns[3].ReplaceAllString(redacted, `${1}[REDACTED]`)
	redacted = redactionPatterns[4].ReplaceAllString(redacted, `${1}[REDACTED]@`)
	return redacted
}
