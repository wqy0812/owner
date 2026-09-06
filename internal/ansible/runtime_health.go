package ansible

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CheckRuntime imports the bundled plugins using the interpreter reported by
// Ansible. It neither opens an inventory nor runs any target-side task.
func (r *Runner) CheckRuntime(ctx context.Context) error {
	binary := r.Binary
	if binary == "" {
		binary = "ansible-playbook"
	}
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return fmt.Errorf("cannot inspect configured Ansible runtime: %w", err)
	}
	match := regexp.MustCompile(`(?m)^\s*python version = .+ \((/[^\r\n]+)\)\s*$`).FindStringSubmatch(string(output))
	python := ""
	if len(match) == 2 {
		python = match[1]
	} else {
		executable, e := exec.LookPath(binary)
		if e == nil {
			source, e := os.ReadFile(executable)
			if e == nil {
				line := strings.SplitN(string(source), "\n", 2)[0]
				if strings.HasPrefix(line, "#!/") && !strings.Contains(line, " ") {
					python = strings.TrimPrefix(line, "#!")
				}
			}
		}
	}
	if python == "" {
		return fmt.Errorf("cannot determine configured Ansible Python interpreter; repair the executable or service environment")
	}
	root, err := os.MkdirTemp("", "clusterforge-runtime-health-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	err = fs.WalkDir(jobPlugins, "job_plugins", func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		data, e := jobPlugins.ReadFile(name)
		if e != nil {
			return e
		}
		target := filepath.Join(root, strings.TrimPrefix(name, "job_plugins/"))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		return os.WriteFile(target, data, 0600)
	})
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, python, "-B", "-c", `import sys, importlib
if sys.version_info < (3,6): raise RuntimeError('native jobs require Python 3.6 or newer')
import multiprocessing
from ansible.release import __version__
if sys.platform == 'darwin' and __version__.startswith('2.8.') and multiprocessing.get_start_method() != 'fork':
 raise RuntimeError('Ansible 2.8 on macOS requires a fork-based Python control environment; configure the executor runtime before execution')
sys.path.insert(0,sys.argv[1])
for name in ('action_plugins.cf_gate','callback_plugins.cf_events','cf_compatibility','cf_native','cf_recovery','cf_media','cf_resources'):
 importlib.import_module(name)
`, root)
	diagnostic, err := command.CombinedOutput()
	if err != nil {
		if len(diagnostic) > 4000 {
			diagnostic = diagnostic[len(diagnostic)-4000:]
		}
		return fmt.Errorf("bundled Ansible action/callback plugins cannot load in %s: %w: %s", python, err, strings.TrimSpace(string(diagnostic)))
	}
	return nil
}
