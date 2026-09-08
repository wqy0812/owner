#!/usr/bin/env python3
"""Exercise deployment refusal and rollback without touching Docker or user data."""
import argparse
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("local_deploy", Path(__file__).with_name("deploy-local-docker.py"))
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


class SourceTests(unittest.TestCase):
    def test_dirty_untracked_deleted_and_permissions_change_fingerprint(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "tracked").write_text("initial")
            subprocess.run(["git", "-C", str(root), "add", "tracked"], check=True)
            def digest():
                return deploy.source_digest(root, deploy.source_files(root))
            values = [digest()]
            (root / "tracked").write_text("changed")
            values.append(digest())
            (root / "untracked").write_text("new source")
            values.append(digest())
            (root / "tracked").chmod(0o755)
            values.append(digest())
            (root / "tracked").unlink()
            values.append(digest())
            self.assertEqual(len(set(values)), len(values))

    def test_external_source_symlink_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "outside").symlink_to("/etc/hosts")
            with self.assertRaisesRegex(RuntimeError, "leaves the repository"):
                deploy.source_digest(root, [Path("outside")])

    def test_internal_symlink_works_when_repository_root_has_an_alias(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            root = parent / "source"
            root.mkdir()
            (root / "file").write_text("retained")
            (root / "link").symlink_to("file")
            alias = parent / "alias"
            alias.symlink_to(root, target_is_directory=True)
            paths = [Path("file"), Path("link")]
            self.assertEqual(deploy.source_digest(root, paths), deploy.source_digest(alias, paths))


def target():
    return {"State": {"Running": True}, "Config": {
        "Image": deploy.IMAGE,
        "Labels": {"com.docker.compose.project": "clusterforge-test", "com.docker.compose.service": "ubuntu",
                   "com.docker.compose.project.config_files": "/config/compose.yaml"},
        "Env": ["NEWPLATFORM_DB_PATH=" + deploy.DB,
                "NEWPLATFORM_ANSIBLE_BIN=/opt/ansible/bin/ansible-playbook"]},
        "HostConfig": {"PortBindings": {"22/tcp": [{"HostIp": "127.0.0.1", "HostPort": "22222"}],
                                         "8080/tcp": [{"HostIp": "127.0.0.1", "HostPort": "8080"}]}},
        "Mounts": [{"Type": "volume", "Name": "volume-" + str(index), "Destination": path}
                   for index, path in enumerate(["/workspace", "/var/lib/clusterforge-test", "/go/pkg/mod", "/var/cache/go-build"])]}


class TargetTests(unittest.TestCase):
    def test_named_volumes_are_reused(self):
        volumes = deploy.validate_target(target(), Path("/config/compose.yaml"), "schema")
        self.assertEqual(volumes["/var/lib/clusterforge-test"], "volume-1")

    def test_wrong_runtime_database_compose_or_missing_persistence_is_rejected(self):
        changes = [lambda v: v["State"].update(Running=False),
                   lambda v: v["Config"].update(Image="ansible:2.18.6"),
                   lambda v: v["Config"].update(Env=["NEWPLATFORM_DB_PATH=/other.db"]),
                   lambda v: v["Config"]["Labels"].update({"com.docker.compose.project.config_files": "/other/compose.yaml"}),
                   lambda v: v["Mounts"][1].update(Type="bind")]
        for change in changes:
            with self.subTest(change=change):
                value = copy.deepcopy(target())
                change(value)
                with self.assertRaises(RuntimeError):
                    deploy.validate_target(value, Path("/config/compose.yaml"), "schema")


class FakeDeployment(deploy.Deployment):
    def __init__(self, root, fail=None):
        self.options = argparse.Namespace(check=False, skip_tests=True)
        self.record = root
        self.compose = root / "compose.yaml"
        self.compose.write_bytes(b"original configuration")
        self.compose_bytes = self.compose.read_bytes()
        self.resolved_compose = "resolved configuration"
        self.compose_cmd = ["fake-compose"]
        self.identifier = "test"
        self.old_image = "sha256:original"
        self.new_image = "sha256:candidate"
        self.old_container = "original-container"
        self.current_image = self.old_image
        self.contract = "required-schema"
        self.volumes = {"/var/lib/clusterforge-test": "existing-state"}
        self.stopped = False
        self.config_changed = False
        self.candidate_created = False
        self.fail = fail
        self.events = []

    def announce(self, message):
        pass

    def check_source(self):
        if self.fail == "source":
            raise RuntimeError("Workspace changed")

    def inspect(self):
        value = target()
        value.update(Id=self.old_container, Image=self.current_image)
        value["Config"]["Labels"]["com.docker.compose.project.config_files"] = str(self.compose)
        self.volumes = {mount["Destination"]: mount["Name"] for mount in value["Mounts"]}
        return value

    def docker_run(self, *args, **kwargs):
        self.events.append(args)
        if args[0] == "tag" and args[2] == deploy.IMAGE:
            self.current_image = args[1]
        if args[:2] == ("exec", deploy.CONTAINER) and "active-work" in args:
            return "run-1\trunning" if self.fail == "active" else ""
        return ""

    def offline(self, *args):
        self.events.append(("offline", *args))
        if "active-work" in args and self.fail == "late-active":
            return "run-created-before-stop\tqueued"
        if "verify" in args and self.fail == "schema":
            raise RuntimeError("unexpected schema contract")
        if "snapshot" in args and self.fail == "backup":
            raise RuntimeError("snapshot failed")
        return ""

    def save_configuration(self):
        self.events.append(("save-configuration",))

    def sync_configuration(self):
        self.events.append(("sync-configuration",))

    def wait_ready(self, container, timeout=45):
        if self.fail == "health" and self.current_image == self.new_image:
            raise RuntimeError("not ready")

    def verify_image(self, container, name):
        if self.fail == "artifact":
            raise RuntimeError("asset mismatch")


class RuntimeBuildTests(unittest.TestCase):
    def test_runtime_edits_replace_cached_image_before_building_the_application(self):
        with tempfile.TemporaryDirectory() as temporary:
            task = FakeDeployment(Path(temporary))
            task.build = Path(temporary) / "image"
            task.build.mkdir()
            task.candidate = "candidate"
            task.volumes = {"/go/pkg/mod": "modules", "/var/cache/go-build": "build-cache"}
            dockerfile = task.build / "Dockerfile.runtime"
            runtime_id = "sha256:old-cached-runtime"
            tags = {}
            application_bases = []

            def docker(*args, **kwargs):
                nonlocal runtime_id
                if args[:2] == ("build", "-f"):
                    runtime_id = "sha256:" + hashlib.sha256(dockerfile.read_bytes()).hexdigest()
                elif args[:2] == ("image", "inspect"):
                    return json.dumps([{"Id": runtime_id if args[2] == deploy.RUNTIME else "sha256:application"}])
                elif args[0] == "tag":
                    tags[args[2]] = args[1]
                elif args[:2] == ("build", "--build-arg"):
                    application_bases.append(tags[args[2].split("=", 1)[1]])
                return ""

            with patch.object(task, "docker_run", side_effect=docker):
                for content in ["FROM runtime-v1\n", "FROM runtime-v2\n"]:
                    dockerfile.write_text(content)
                    task.build_image()
                    self.assertEqual(application_bases[-1], "sha256:" + hashlib.sha256(content.encode()).hexdigest())
            self.assertNotEqual(*application_bases)

    def test_failed_runtime_rebuild_does_not_fall_back_to_the_old_image(self):
        with tempfile.TemporaryDirectory() as temporary:
            task = FakeDeployment(Path(temporary))
            task.build = Path(temporary)
            with patch.object(task, "docker_run", side_effect=RuntimeError("runtime build failed")) as docker:
                with self.assertRaisesRegex(RuntimeError, "runtime build failed"):
                    task.build_image()
            self.assertEqual(docker.call_count, 1)
            self.assertEqual(docker.call_args.args[0], "build")
            self.assertFalse(task.candidate_created)
            self.assertFalse(task.stopped)


class ActivationTests(unittest.TestCase):
    def test_active_work_or_changed_source_never_stops_service(self):
        for failure in ["active", "source"]:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary, patch.object(deploy, "run", return_value="resolved configuration"):
                task = FakeDeployment(Path(temporary), failure)
                with self.assertRaises(RuntimeError):
                    task.activate()
                self.assertFalse(task.stopped)
                self.assertFalse(any(event[0] == "stop" for event in task.events))

    def test_offline_gates_precede_image_change_and_failures_restore_exact_image(self):
        for failure in ["late-active", "schema", "backup", "health", "artifact"]:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary, patch.object(deploy, "run", return_value="resolved configuration"):
                task = FakeDeployment(Path(temporary), failure)
                with self.assertRaises(RuntimeError):
                    task.activate()
                task.rollback()
                self.assertEqual(task.current_image, task.old_image)
                self.assertEqual(json.loads((task.record / "result.json").read_text())["databaseRestored"], False)
                if failure in ["late-active", "schema", "backup"]:
                    self.assertNotIn(("tag", task.new_image, deploy.IMAGE), task.events)
                self.assertFalse(any("restore" in str(event) or "down" in event for event in task.events))

    def test_success_keeps_database_and_backs_up_before_switch(self):
        with tempfile.TemporaryDirectory() as temporary, patch.object(deploy, "run", return_value="resolved configuration"):
            task = FakeDeployment(Path(temporary))
            task.activate()
            self.assertFalse(task.stopped)
            backup_index = next(i for i, event in enumerate(task.events) if "snapshot" in event)
            switch_index = task.events.index(("tag", task.new_image, deploy.IMAGE))
            self.assertLess(backup_index, switch_index)
            self.assertEqual(task.current_image, task.new_image)
            self.assertEqual(json.loads((task.record / "result.json").read_text())["status"], "deployed")


if __name__ == "__main__":
    unittest.main()
