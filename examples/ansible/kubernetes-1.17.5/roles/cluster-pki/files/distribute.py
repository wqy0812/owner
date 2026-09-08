import base64
import json
import os
import pathlib

bundle = json.loads(base64.b64decode(os.environ['CF_PKI_BUNDLE']))
root = pathlib.Path('/etc/kubernetes')
for relative, content in bundle.items():
    name = pathlib.PurePosixPath(relative)
    if name.is_absolute() or '..' in name.parts or not name.parts:
        raise RuntimeError('Invalid certificate destination')
    target = root.joinpath(*name.parts)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content)
    os.chmod(str(target), 0o600)
