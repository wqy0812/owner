"""Read-only validation of the sealed resource policy before native startup."""
import posixpath
import re

PARAM = re.compile(r'\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}')


def child(parent, value):
    return value == parent or value.startswith(parent + '/')


def resolve(value, values):
    def substitute(match):
        name = match.group(1) or match.group(2)
        result = values.get(name)
        if not isinstance(result, str) or not result:
            raise ValueError('unresolved resource parameter: ' + name)
        return result
    result = PARAM.sub(substitute, value)
    if not result.startswith('/') or result == '/' or posixpath.normpath(result) != result or any(c in result for c in '\x00\\*?[]{}$'):
        raise ValueError('invalid resolved resource path')
    return result


def overlap(a, b):
    if a['path'] == b['path']:
        return True
    for parent, nested in ((a, b), (b, a)):
        if parent['scope'] == 'tree' and child(parent['path'], nested['path']):
            return not any(child(excluded, nested['path']) for excluded in parent.get('excludes') or [])
    return False


def read_covered(consumer, provider):
    if not child(provider['path'], consumer['path']) or any(child(p, consumer['path']) for p in provider.get('excludes') or []):
        return False
    if provider['scope'] == 'file':
        return consumer['scope'] == 'file' and consumer['path'] == provider['path']
    if consumer['scope'] == 'tree':
        for excluded in provider.get('excludes') or []:
            if child(consumer['path'], excluded) and not any(child(p, excluded) for p in consumer.get('excludes') or []):
                return False
    return True


def sharing(nested, parent):
    a, b = nested['claim'], parent['claim']
    ref = a.get('sharedWith') or {}
    if ref.get('releaseId') != parent['releaseId'] or ref.get('claimId') != b['id']:
        return False
    if a['access'] == 'read':
        return read_covered(a, b)
    return a['path'] != b['path'] and any(child(prefix, a['path']) for prefix in b.get('sharedPaths') or [])


def hosts_overlap(a, b):
    return bool(set(a.get('hosts') or []) & set(b.get('hosts') or []))


def validate(plan):
    metadata = plan.get('metadata') or {}
    version = metadata.get('resourcePolicyVersion', 0)
    if version == 0:
        return  # Original locked jobs retain their original policy.
    if version != 1:
        raise ValueError('unsupported resource policy')
    original = {s['id']: s for s in metadata.get('steps') or []}
    instances = list(metadata.get('existingResources') or [])
    for step in plan['steps']:
        source = original.get(step['id'], {})
        if step.get('sourceType') == 'scenario_acceptance' or source.get('sourceParametersFrozen') or (step.get('action') in ('rollback', 'uninstall') and source.get('backupRef')):
            continue
        contract = step.get('resourceContract')
        if not contract or contract.get('version') != 1:
            raise ValueError('action resource declaration missing: ' + step['id'])
        claims = contract.get('claims') or []
        if bool(contract.get('noManagedPaths')) == bool(claims):
            raise ValueError('resource declaration is incomplete')
        resolved = step.get('resources') or []
        if len(resolved) != len(claims):
            raise ValueError('resolved resource list differs from sealed declaration')
        for claim, instance in zip(claims, resolved):
            expected = dict(claim)
            expected['path'] = resolve(claim['path'], step.get('variables') or {})
            for field in ('excludes', 'sharedPaths'):
                values = [resolve(v, step.get('variables') or {}) for v in claim.get(field) or []]
                if any(v == expected['path'] or not child(expected['path'], v) for v in values):
                    raise ValueError('resource subpath escapes its declaration')
                if values: expected[field] = values
                else: expected.pop(field, None)
            actual = dict(instance['claim'])
            for field in ('excludes', 'sharedPaths'):
                if not actual.get(field): actual.pop(field, None)
            if actual != expected or not instance.get('hosts'):
                raise ValueError('resource resolution or concrete hosts changed')
            instances.append(instance)
    for index, a in enumerate(instances):
        if a['claim'].get('sharedWith'):
            covered = set()
            for b in instances:
                if sharing(a, b):
                    covered.update(b.get('hosts') or [])
            if not covered or not set(a.get('hosts') or []).issubset(covered):
                raise ValueError('unresolved shared resource source')
        for b in instances[index+1:]:
            if a['ownerId'] == b['ownerId'] and a['releaseId'] == b['releaseId']:
                continue
            if not hosts_overlap(a, b) or not overlap(a['claim'], b['claim']):
                continue
            left, right = a['claim'], b['claim']
            exclusive = (left.get('exclusive') and right['access'] == 'manage') or (right.get('exclusive') and left['access'] == 'manage')
            if not exclusive and (sharing(a, b) or sharing(b, a)):
                continue
            if exclusive or (left['access'] == 'manage' and right['access'] == 'manage') or set((left['access'], right['access'])) == {'read', 'manage'}:
                raise ValueError('overlapping resource ownership: ' + left['path'] + ' / ' + right['path'])
