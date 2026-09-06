"""Pure recovery rules, shared by the platform and native Ansible plugins.

Python 3.6 compatible. This module never executes tasks or accesses a platform.
"""
import copy
import json
import sys


def instance(step):
    return step.get('componentId', '') + '/' + step.get('nodeId', '')


def continuation(plan, start):
    steps = plan['steps']
    if start < 0 or start > len(steps):
        raise ValueError('invalid continuation boundary')
    pending = set(instance(s) for s in steps[start:])
    # Completed teardown checks were acknowledged while their providers still
    # existed. Later rollback steps can remove those providers (e.g. the API
    # server), so replaying these checks cannot establish a current baseline.
    # Keep their receipts and only run checks for incomplete teardown actions.
    teardown = set((instance(s), s.get('actionId')) for s in steps[:start]
                   if s['phase'] == 'execute' and s.get('action') in ('rollback', 'uninstall'))
    latest = {}
    for index, step in enumerate(steps[:start]):
        if (step['phase'] in ('post', 'check') and
                (instance(step), step.get('parentActionId')) not in teardown):
            latest[instance(step)] = index
    result = []
    for index, step in enumerate(steps[:start]):
        if latest.get(instance(step)) == index and instance(step) not in pending:
            step = copy.deepcopy(step)
            if not step['id'].startswith('refresh-'):
                step['id'] = 'refresh-' + step['id']
            step['phase'] = 'check'
            result.append(step)
    return result + copy.deepcopy(steps[start:])


def retry(plan, results):
    completed = dict((r['stepId'], r['status'] == 'succeeded') for r in (results or []))
    start = next((i for i, s in enumerate(plan['steps']) if not completed.get(s['id'])), len(plan['steps']))
    if start == len(plan['steps']):
        raise ValueError('job has no incomplete stages')
    step = plan['steps'][start]
    if step.get('sourceType') == 'scenario_acceptance' and not step.get('retrySafe'):
        raise ValueError('mutating scenario acceptance cannot be replayed automatically; inspect its preserved resources')
    if step['phase'] == 'execute':
        if not step.get('retrySafe'):
            raise ValueError('failed action is not declared safe to retry; inspect or roll back')
        has_pre = (start > 0 and plan['steps'][start - 1]['phase'] == 'pre' and
                   plan['steps'][start - 1].get('parentActionId') == step.get('actionId') and
                   instance(plan['steps'][start - 1]) == instance(step))
        if has_pre:
            start -= 1
        elif step.get('action') != 'rollback' or step.get('preCheckRequired'):
            raise ValueError('retry action has no precheck')
    return continuation(plan, start)


def rollback(plan, actions, nodes=None):
    stages = [s for s in (plan.get('recovery') or [])
              if actions.get(s.get('recoveryOfStepId')) not in (None, '', 'rolled_back')]
    if not stages:
        raise ValueError('no touched component remains to roll back')
    if not nodes:
        return stages
    requested = set(nodes)
    if len(requested) != len(nodes) or '' in requested:
        raise ValueError('rollback nodes must be nonempty and unique')
    result, seen, omitted = [], set(), False
    for step in stages:
        key = instance(step)
        if key not in requested:
            omitted = True
            continue
        if omitted:
            raise ValueError('rollback scope must include preceding dependent nodes')
        seen.add(key)
        result.append(step)
    if seen != requested:
        raise ValueError('rollback scope contains an unknown component instance')
    return result


def boundary_status(phase, boundary):
    if boundary not in ('begin', 'end'):
        raise ValueError('unknown phase boundary')
    if phase == 'execute':
        return 'started' if boundary == 'begin' else 'main_succeeded'
    if phase == 'post' and boundary == 'end':
        return 'verified'
    return ''


def action_transition(actions, original, boundary, step):
    """Only acknowledged boundaries advance durable action receipts."""
    actions = dict(actions)
    if step['phase'] == 'execute':
        actions[step['id']] = boundary_status(step['phase'], boundary)
    if step['phase'] == 'post' and boundary == 'end':
        parents = [s for s in (original.get('steps') or []) + (original.get('recovery') or [])
                   if s['phase'] == 'execute' and instance(s) == instance(step)
                   and s.get('actionId') == step.get('parentActionId')
                   and s.get('recoveryOfStepId') == step.get('recoveryOfStepId')]
        if not parents:
            raise ValueError('post-check has no locked parent action')
        parent = parents[0]
        if actions.get(parent['id']) not in ('main_succeeded', 'verified'):
            raise ValueError('post-check has no acknowledged successful main action')
        actions[parent['id']] = boundary_status(step['phase'], boundary)
        if parent['action'] in ('rollback', 'uninstall'):
            if parent.get('recoveryOfStepId'):
                actions[parent['recoveryOfStepId']] = 'rolled_back'
            ref = parent.get('variables', {}).get('clusterforge_backup_ref')
            if ref:
                for other in original.get('steps', []):
                    values = other.get('variables', {})
                    if other['phase'] == 'execute' and values.get('clusterforge_backup_operation') == 'capture' and values.get('clusterforge_backup_ref') == ref:
                        actions[other['id']] = 'rolled_back'
    return actions


def evaluate(request):
    operation = request['operation']
    if operation == 'boundary-status':
        return boundary_status(request['phase'], request['boundary'])
    if operation == 'retry':
        return retry(request['plan'], request['results'])
    if operation == 'continuation':
        return continuation(request['plan'], request['start'])
    if operation == 'transition':
        return action_transition(request['actions'], request['plan'], request['boundary'], request['step'])
    raise ValueError('unknown recovery operation')


if __name__ == '__main__':
    try:
        print(json.dumps({'result': evaluate(json.load(sys.stdin))}, ensure_ascii=False))
    except Exception as error:
        print(json.dumps({'error': str(error)}))
        sys.exit(1)
