"""Run the mandatory real Ansible tests and reject skips or an empty selection."""
import json
import os
import subprocess
import sys

PACKAGES = ['./internal/ansible', './internal/service', './internal/jobcli', './internal/sshcheck']
PATTERN = ('TestNativeJobReal|TestRoleJobRealAnsible|TestRoleJobAcceptance|'
           'TestScenarioBusinessAcceptanceRealAnsible|TestScenarioAcceptanceCredentialIsolationRealAnsible|'
           'TestScenarioRoleJobContinuationRealAnsible|TestStandaloneReal|TestYAMLTwoStepRollbackRealAnsible|'
           'TestNativeRollbackResumeAfterProviderRemoval|TestResetTaskTargetsRealAnsible|'
           'TestExecutorHealthRealRuntimePlugins|TestImageCommandProbeRealAnsible')


def validate_results(events, status):
    passed = {(e['Package'], e['Test']) for e in events if e.get('Action') == 'pass' and e.get('Test') and '/' not in e['Test']}
    skipped = [e.get('Test', e.get('Package')) for e in events if e.get('Action') == 'skip']
    if status or skipped or any(e.get('Action') == 'fail' for e in events):
        raise RuntimeError('Role gate failed or skipped tests: ' + ', '.join(skipped))
    for package in PACKAGES:
        if not any(p.endswith(package[1:]) for p, _ in passed):
            raise RuntimeError('Role gate did not run tests in ' + package)
    if not any(name == 'TestImageCommandProbeRealAnsible' for _, name in passed):
        raise RuntimeError('The SSH image prerequisite test did not run')
    return len(passed)


def main():
    binary = os.environ['ANSIBLE_PLAYBOOK']
    version = subprocess.check_output([binary, '--version'], universal_newlines=True)
    if version.splitlines()[0] != 'ansible-playbook 2.8.8' or 'python version = 3.6.9 ' not in version:
        raise RuntimeError('Use the fixed Ansible 2.8.8 / Python 3.6.9 container')
    env = dict(os.environ, CLUSTERFORGE_JOB_TEST_ANSIBLE=binary,
               CLUSTERFORGE_JOB_CLI=os.environ['CLUSTERFORGE_JOB_CLI'])
    process = subprocess.Popen([os.environ.get('GO', 'go'), 'test'] + PACKAGES +
                               ['-run', PATTERN, '-count=1', '-json'], env=env,
                               stdout=subprocess.PIPE, universal_newlines=True)
    events = []
    for line in process.stdout:
        print(line, end='', flush=True)
        events.append(json.loads(line))
    count = validate_results(events, process.wait())
    print('Role gate passed: {} top-level tests, 0 skipped'.format(count))


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, KeyError, subprocess.CalledProcessError) as error:
        print('error: ' + str(error), file=sys.stderr)
        sys.exit(1)
