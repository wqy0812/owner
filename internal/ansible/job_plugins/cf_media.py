"""Native delivery of the exact artifacts/images selected in a locked plan."""
import hashlib
import json
import subprocess
import urllib.error
import urllib.request


def request(url, payload=None):
    data = json.dumps(payload).encode('utf-8') if payload is not None else None
    query = urllib.request.Request(url, data=data, headers={'Content-Type': 'application/json'})
    return urllib.request.urlopen(query, timeout=1800)


def artifact(url, expected, size=0):
    sha, observed = hashlib.sha256(), 0
    with request(url) as stream:
        while True:
            block = stream.read(1024 * 1024)
            if not block: break
            sha.update(block)
            observed += len(block)
    if sha.hexdigest() != expected.replace('sha256:', '', 1):
        raise ValueError('artifact SHA-256 mismatch: ' + url)
    if size and observed != size:
        raise ValueError('artifact size mismatch: ' + url)


def docker(*args):
    result = subprocess.run(['docker'] + list(args), stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=1800)
    if result.returncode:
        raise ValueError('docker ' + args[0] + ' failed: ' + result.stderr.decode('utf-8', 'replace')[-4096:])
    return result.stdout


def image(ref, expected):
    observed = json.loads(docker('manifest', 'inspect', '--insecure', '--verbose', ref))
    if not isinstance(observed, dict):
        raise ValueError('image must resolve to one locked platform manifest: ' + ref)
    actual = observed.get('Descriptor', {}).get('digest')
    expected = expected.split('@')[-1]
    if actual != expected:
        raise ValueError('image digest mismatch: ' + ref)


def prepare(metadata):
    requirements = metadata.get('deliveryRequirements') or []
    decisions = dict((d['requirementId'], d['mode']) for d in metadata.get('deliveryDecisions') or [])
    if any(decisions.get(r['id']) not in ('direct', 'transfer') for r in requirements):
        raise ValueError('media delivery choices are not completely locked')
    for transfer in metadata.get('artifactTransfers') or []:
        base = 'http://' + transfer['targetStation'] + '/api/v1/'
        payload = dict(path=transfer['relativePath'], sha256=transfer['sha256'])
        try:
            with request(base + 'register', payload): pass
        except urllib.error.HTTPError as error:
            if error.code not in (404, 422): raise
            artifact(transfer['sourceUrl'], transfer['sha256'], transfer.get('sizeBytes', 0))
            with request(base + 'fetch', dict(payload, sourceUrl=transfer['sourceUrl'])) as response:
                value = json.load(response)
            if value.get('sha256') != transfer['sha256'] or value.get('relativePath') != transfer['relativePath']:
                raise ValueError('file station returned mismatched artifact identity')
            with request(base + 'register', payload): pass
    for transfer in metadata.get('imageTransfers') or []:
        try:
            image(transfer['targetDigest'], transfer['targetDigest'])
        except ValueError as error:
            message = str(error).lower()
            if not any(text in message for text in ('no such manifest', 'manifest unknown', 'name unknown', 'not found')):
                raise
            image(transfer['sourceDigest'], transfer['sourceDigest'])
            docker('pull', transfer['sourceDigest'])
            docker('tag', transfer['sourceDigest'], transfer['targetRef'])
            docker('push', transfer['targetRef'])
            image(transfer['targetDigest'], transfer['targetDigest'])
    for requirement in requirements:
        location = requirement['target'] if decisions[requirement['id']] == 'transfer' else requirement['source']
        if requirement['kind'] == 'artifact':
            artifact(location, requirement['identity'], requirement.get('sizeBytes', 0))
        elif requirement['kind'] == 'image':
            image(location, requirement['identity'])
        else:
            raise ValueError('unknown media kind')


def verify_steps(steps):
    seen = set()
    for step in steps:
        for media in step.get('media') or []:
            identity = (media['kind'], media['location'], media['identity'], media.get('sizeBytes', 0))
            if identity in seen: continue
            seen.add(identity)
            if media['kind'] == 'artifact':
                artifact(media['location'], media['identity'], media.get('sizeBytes', 0))
            elif media['kind'] == 'image':
                image(media['location'], media['identity'])
            else:
                raise ValueError('unknown locked media kind')
