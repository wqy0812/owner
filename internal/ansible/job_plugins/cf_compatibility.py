"""Check old-runtime action dispatch without executing component tasks."""
import os
from ansible import __version__
from ansible.parsing.dataloader import DataLoader
from ansible.parsing.mod_args import ModuleArgsParser
from ansible.plugins.loader import action_loader


def validate(root):
    if not __version__.startswith('2.8.'):
        return
    loader = DataLoader()
    def tasks(items, path):
        if not isinstance(items, list):
            raise ValueError('role task file must contain a list: ' + path)
        for task in items:
            if any(key in task for key in ('block', 'always', 'rescue')):
                for key in ('block', 'always', 'rescue'):
                    if key in task: tasks(task[key], path)
                continue
            action, arguments, _ = ModuleArgsParser(task).parse()
            short = action.replace('ansible.builtin.', '', 1)
            location = path + ' / ' + str(task.get('name', action))
            if action.startswith('ansible.builtin.') and action_loader.find_plugin(short) and not action_loader.find_plugin(action):
                raise ValueError('Ansible 2.8.8 cannot dispatch ' + action + '; use ' + short + ' in ' + location)
            plugin = action_loader.get(action, class_only=True)
            valid = getattr(plugin, '_VALID_ARGS', None) if plugin else None
            # 2.8 script reads _raw_params directly and has no populated
            # _VALID_ARGS; later cmd syntax otherwise fails only at dispatch.
            if action == 'script':
                valid = {'_raw_params', 'creates', 'removes', 'chdir', 'executable'}
            if valid:
                invalid = set(arguments) - set(valid)
                if invalid:
                    raise ValueError('Ansible 2.8.8 does not support action arguments ' + ', '.join(sorted(invalid)) + ' in ' + location)
    roles = os.path.join(root, 'roles')
    for base, directories, names in os.walk(roles):
        directories.sort()
        for name in sorted(names):
            relative = os.path.relpath(os.path.join(base, name), roles)
            parts = relative.split(os.sep)
            if len(parts) >= 3 and parts[1] in ('tasks', 'handlers') and name.endswith(('.yml', '.yaml')):
                tasks(loader.load_from_file(os.path.join(base, name)), 'roles/' + relative)
