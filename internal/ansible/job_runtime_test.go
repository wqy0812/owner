package ansible

import "testing"

func TestJobRuntimeVersionFormats(t *testing.T) {
	for _, tc := range []struct{ name, output, ansible, python string }{
		{"legacy", "ansible-playbook 2.8.8\n  config file = None\n  python version = 3.6.9 (default, Mar 10 2023, 16:46:00) [GCC 8.4.0]\n", "2.8.8", "3.6.9"},
		{"modern", "ansible-playbook [core 2.19.12]\n  python version = 3.12.14 (main)\n", "2.19.12", "3.12.14"},
		{"no python", "ansible-playbook 2.8.8\n", "", ""},
		{"wrong executable", "ansible 2.8.8\n  python version = 3.6.9\n", "", ""},
		{"malformed", "ansible-playbook 2.8.8broken\n  python version = 3.6.9\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, err := parseJobRuntime(tc.output)
			if tc.ansible == "" {
				if err == nil {
					t.Fatal("accepted unidentified runtime")
				}
				return
			}
			if err != nil || runtime.AnsibleCore != tc.ansible || runtime.Python != tc.python {
				t.Fatalf("runtime=%+v error=%v", runtime, err)
			}
		})
	}
}
