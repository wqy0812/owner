#!/usr/bin/env python3
"""Deploy the current checkout to the existing local Ansible 2.8.8 container."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
CONTAINER = "clusterforge-test-ubuntu"
IMAGE = "clusterforge-test-ubuntu:18.04-ansible2.8.8"
RUNTIME = IMAGE + "-runtime"
DB = "/var/lib/clusterforge-test/platform/platform.db"
TOOL = "/opt/clusterforge/platform/clusterforge-backup"
HEALTH = "/usr/local/bin/clusterforge-test-healthcheck"


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(args, *, cwd=None, log=None, stdin=None, env=None):
    if log:
        with Path(log).open("w") as output:
            result = subprocess.run(args, cwd=cwd, env=env, stdin=stdin,
                                    stdout=output, stderr=subprocess.STDOUT)
        if result.returncode:
            tail = Path(log).read_text(errors="replace").splitlines()[-25:]
            raise RuntimeError("Command failed: " + " ".join(map(str, args[:5]))
                               + "\n" + "\n".join(tail) + "\nLog: " + str(log))
        return ""
    return subprocess.check_output(args, cwd=cwd, env=env, stdin=stdin,
                                   text=True, stderr=subprocess.PIPE).strip()


def source_files(root):
    names = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=root)
    return [Path(os.fsdecode(name)) for name in sorted(set(names.split(b"\0")) - {b""})]


def source_digest(root, files):
    root = root.resolve()
    digest = hashlib.sha256()
    for relative in files:
        path = root / relative
        digest.update(os.fsencode(str(relative)) + b"\0")
        if not path.exists() and not path.is_symlink():
            digest.update(b"deleted\0")
            continue
        require(not path.is_symlink() or path.resolve().is_relative_to(root),
                "Source symlink leaves the repository: " + str(relative))
        digest.update(str(path.lstat().st_mode).encode() + b"\0")
        digest.update(os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def validate_target(info, compose, contract):
    require(info["State"]["Running"], "Start the existing local Docker container first")
    require(info["Config"]["Image"] == IMAGE, "Expected the existing Ansible 2.8.8 image")
    labels = info["Config"]["Labels"]
    require(labels.get("com.docker.compose.project") == "clusterforge-test"
            and labels.get("com.docker.compose.service") == "ubuntu", "Unexpected Compose service")
    require(labels.get("com.docker.compose.project.config_files") == str(compose),
            "The container must use the expected single Compose configuration")
    variables = dict(value.split("=", 1) for value in info["Config"]["Env"] if "=" in value)
    require(variables.get("NEWPLATFORM_DB_PATH") == DB, "Unexpected database path; refusing deployment")
    require(variables.get("NEWPLATFORM_ANSIBLE_BIN") == "/opt/ansible/bin/ansible-playbook",
            "Unexpected configured Ansible executable")
    for port, published in [("22/tcp", "22222"), ("8080/tcp", "8080")]:
        require(info["HostConfig"]["PortBindings"].get(port) == [
            {"HostIp": "127.0.0.1", "HostPort": published}], "Unexpected local port binding: " + port)
    mounts = {mount["Destination"]: mount for mount in info["Mounts"]}
    for destination in ["/workspace", "/var/lib/clusterforge-test", "/go/pkg/mod", "/var/cache/go-build"]:
        require(mounts.get(destination, {}).get("Type") == "volume",
                "Expected persistent named volume: " + destination)
    require(contract != "", "The checkout has no schema contract")
    return {destination: mount["Name"] for destination, mount in mounts.items() if mount["Type"] == "volume"}


class Deployment:
    def __init__(self, options):
        self.options = options
        self.config = Path.home() / ".local/share/clusterforge-test-env/ubuntu"
        self.compose = self.config / "compose.yaml"
        self.context = run(["docker", "context", "show"])
        endpoint = json.loads(run(["docker", "context", "inspect", self.context]))[0]["Endpoints"]["docker"]["Host"]
        require(endpoint.startswith("unix://"), "This script only deploys to a local Unix Docker endpoint")
        require(not os.environ.get("DOCKER_HOST") or os.environ["DOCKER_HOST"] == endpoint,
                "DOCKER_HOST differs from the selected context; choose the local context explicitly")
        self.docker = ["docker", "--context", self.context]
        self.compose_cmd = [*self.docker, "compose", "-f", str(self.compose)]
        self.contract = re.search(r'schemaContract = "([^"]+)"',
                                  (ROOT / "internal/store/schema.go").read_text())[1]
        self.identifier = time.strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:8]
        self.record = ROOT / "output/local-docker-deploy" / self.identifier
        self.candidate = "clusterforge-deploy-check-" + self.identifier
        self.candidate_created = False
        self.stopped = False
        self.config_changed = False

    def docker_run(self, *args, **kwargs):
        return run([*self.docker, *args], **kwargs)

    def inspect(self):
        return json.loads(self.docker_run("inspect", CONTAINER))[0]

    def announce(self, message):
        print("==> " + message, flush=True)

    def active_check(self, offline=False):
        command = ["database", "active-work", "--db", DB]
        active = self.offline(*command) if offline else self.docker_run("exec", CONTAINER, TOOL, *command)
        require(not active, "Active work blocks deployment:\n" + active)

    def offline(self, *args):
        return self.docker_run("run", "--rm", "--network", "none", "--mount",
                               "type=volume,src=" + self.volumes["/var/lib/clusterforge-test"]
                               + ",dst=/var/lib/clusterforge-test", "--entrypoint", TOOL,
                               self.old_image, *args)

    def preflight(self):
        self.announce("Checking the existing local container, runtime and database")
        require(self.compose.is_file(), "Missing local Compose configuration: " + str(self.compose))
        info = self.inspect()
        self.volumes = validate_target(info, self.compose, self.contract)
        self.old_container = info["Id"]
        self.old_image = info["Image"]
        self.compose_bytes = self.compose.read_bytes()
        self.docker_run("exec", CONTAINER, "/opt/ansible/bin/python", "-c",
                        "import ansible,sys; assert ansible.__version__ == '2.8.8'; "
                        "assert sys.version_info[:3] == (3,6,9)")
        self.docker_run("exec", CONTAINER, HEALTH)
        self.docker_run("exec", CONTAINER, TOOL, "database", "verify", "--db", DB,
                        "--expected-contract", self.contract)
        self.active_check()
        self.resolved_compose = run([*self.compose_cmd, "config", "--format", "json"])
        resolved = json.loads(self.resolved_compose)
        service = resolved["services"]["ubuntu"]
        require(service["image"] == IMAGE and service["container_name"] == CONTAINER,
                "Compose no longer targets the expected container/image")
        resolved_volumes = {mount["target"]: resolved["volumes"][mount["source"]]["name"]
                            for mount in service.get("volumes", []) if mount["type"] == "volume"}
        require(resolved_volumes == self.volumes, "Compose would change the existing data volumes")
        print("Ready: Ansible 2.8.8 / Python 3.6.9 / " + self.contract, flush=True)

    def check_source(self):
        require(source_digest(ROOT, source_files(ROOT)) == self.source_hash,
                "Workspace changed during deployment; rerun when edits finish")

    def prepare(self, temporary):
        self.record.mkdir(parents=True, mode=0o700)
        self.announce("Logs and backups: " + str(self.record))
        self.files = source_files(ROOT)
        self.source_hash = source_digest(ROOT, self.files)
        self.source = temporary / "source"
        self.source.mkdir()
        self.archive = temporary / "source.tar"
        with tarfile.open(self.archive, "w") as archive:
            for relative in self.files:
                path = ROOT / relative
                if not path.exists() and not path.is_symlink():
                    continue
                target = self.source / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                if path.is_symlink():
                    target.symlink_to(os.readlink(path))
                else:
                    shutil.copy2(path, target)
                archive.add(target, arcname=str(relative), recursive=False)
        require(source_digest(self.source, self.files) == self.source_hash,
                "Source changed while copying the checkout")
        self.check_source()
        require((ROOT / "web/node_modules").is_dir(), "Install frontend dependencies with make bootstrap first")
        (self.source / "web/node_modules").symlink_to(ROOT / "web/node_modules", target_is_directory=True)
        # Relocation is intentional. Never let pnpm reinstall into the shared dependency directory.
        self.frontend_env = {**os.environ, "pnpm_config_verify_deps_before_run": "warn"}
        self.build = temporary / "image"
        shutil.copytree(self.source / "deploy/local-docker", self.build)
        (self.build / "bin").mkdir()
        run(["git", "diff", "--check"], cwd=ROOT)
        self.announce("Building the copied checkout, including uncommitted files")
        run(["make", "build-web"], cwd=self.source, env=self.frontend_env, log=self.record / "build-web.log")
        architecture = json.loads(self.docker_run("image", "inspect", self.old_image))[0]["Architecture"]
        require(architecture in ("arm64", "amd64"), "Unsupported local image architecture")
        for binary, package in [("newplatform", "server"), ("clusterforge-backup", "backup"),
                                ("clusterforge-job", "clusterforge-job")]:
            command = ["go", "build", "-buildvcs=false", "-trimpath"]
            if binary == "newplatform":
                command += ["-tags", "embed"]
            command += ["-o", str(self.build / "bin" / binary), "./cmd/" + package]
            run(command, cwd=self.source, env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": architecture},
                log=self.record / (binary + "-build.log"))
        dist = self.source / "web/dist"
        self.manifest = {
            "gitCommit": run(["git", "rev-parse", "HEAD"], cwd=ROOT),
            "dirty": bool(run(["git", "status", "--porcelain"], cwd=ROOT)),
            "sourceSha256": self.source_hash, "ansible": "2.8.8", "python": "3.6.9",
            "schemaContract": self.contract, "architecture": architecture,
            "uiVersion": json.loads((dist / "version.json").read_text())["version"],
            "binaries": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (self.build / "bin").iterdir()},
            "assets": {p.relative_to(dist).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
                       for p in sorted(dist.rglob("*")) if p.is_file()},
        }
        for target in [self.build / "deployment.json", self.record / "deployment.json"]:
            target.write_text(json.dumps(self.manifest, indent=2) + "\n")
        self.check_source()

    def build_image(self):
        self.announce("Building a candidate with the pinned Ansible 2.8.8 runtime")
        # Docker's layer cache reuses unchanged dependencies while honoring edits
        # to the runtime Dockerfile in this source snapshot.
        self.docker_run("build", "-f", str(self.build / "Dockerfile.runtime"), "-t", RUNTIME,
                        str(self.build), log=self.record / "runtime-build.log")
        runtime_id = json.loads(self.docker_run("image", "inspect", RUNTIME))[0]["Id"]
        base_tag = "clusterforge-test-ubuntu:deploy-base-" + runtime_id.split(":")[1][:16]
        self.docker_run("tag", runtime_id, base_tag)
        self.candidate_tag = "clusterforge-test-ubuntu:deploy-" + self.identifier
        self.docker_run("build", "--build-arg", "RUNTIME_IMAGE=" + base_tag,
                        "-t", self.candidate_tag, str(self.build), log=self.record / "image-build.log")
        self.new_image = json.loads(self.docker_run("image", "inspect", self.candidate_tag))[0]["Id"]
        args = ["run", "-d", "--init", "--name", self.candidate, "--cpus", "2", "--memory", "3g", "--pids-limit", "512"]
        for destination in ["/go/pkg/mod", "/var/cache/go-build"]:
            args += ["--mount", "type=volume,src=" + self.volumes[destination] + ",dst=" + destination]
        self.candidate_created = True
        self.docker_run(*args, self.new_image)
        self.wait_ready(self.candidate)
        self.verify_image(self.candidate, "candidate-verification.json")

    def wait_ready(self, container, timeout=45):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                self.docker_run("exec", container, HEALTH)
                return
            except subprocess.CalledProcessError:
                time.sleep(1)
        self.docker_run("logs", "--tail", "100", container, log=self.record / (container + "-startup.log"))
        raise RuntimeError("Container did not become ready: " + container)

    def verify_image(self, container, name):
        with (self.build / "verify.py").open() as source:
            result = self.docker_run("exec", "-i", container, "python3", "-", json.dumps(self.manifest), stdin=source)
        (self.record / name).write_text(result + "\n")

    def tests(self):
        if self.options.skip_tests:
            self.announce("Skipping project regression; runtime, data and artifact checks remain mandatory")
            return
        self.announce("Running deployment and Go tests")
        run(["python3", "scripts/test-deploy-local-docker.py"], cwd=self.source,
            log=self.record / "deployment-tests.log")
        run(["go", "test", "./..."], cwd=self.source, log=self.record / "go-tests.log")
        self.announce("Running frontend tests")
        run(["pnpm", "--dir", "web", "test"], cwd=self.source, env=self.frontend_env,
            log=self.record / "frontend-tests.log")
        self.docker_run("exec", self.candidate, "mkdir", "-p", "/workspace/source")
        with self.archive.open("rb") as archive:
            self.docker_run("cp", "-", self.candidate + ":/workspace/source", stdin=archive)
        self.announce("Running isolated Ansible 2.8.8 Role and SSH tests")
        self.docker_run("exec", "-w", "/workspace/source", self.candidate,
                        "make", "test-role-job", "ANSIBLE_PLAYBOOK=/opt/ansible/bin/ansible-playbook",
                        log=self.record / "ansible-tests.log")
        self.docker_run("exec", self.candidate, "ansible-playbook", "-i", "/etc/ansible/inventory.ini",
                        "/opt/clusterforge-test/smoke.yml", log=self.record / "ssh-smoke.log")

    def save_configuration(self):
        backup = self.record / "configuration"
        backup.mkdir(mode=0o700)
        self.config_files = ["Dockerfile", "Dockerfile.runtime", "entrypoint.sh", "healthcheck.sh",
                             "inventory.ini", "smoke.yml", "verify.py", "deployment.json", "last-verified.txt"]
        for name in [*self.config_files, "compose.yaml"]:
            if (self.config / name).exists():
                shutil.copy2(self.config / name, backup / name)
        if (self.config / "bin").exists():
            shutil.copytree(self.config / "bin", backup / "bin")

    def sync_configuration(self):
        self.config_changed = True
        # Keep `docker compose build` bound to the same binaries as this deployment.
        runtime = (self.build / "Dockerfile.runtime").read_text()
        application = (self.build / "Dockerfile").read_text().split("FROM ${RUNTIME_IMAGE}\n", 1)[1]
        (self.config / "Dockerfile").write_text(runtime + "\nFROM runtime\n" + application)
        for name in self.config_files:
            if name not in ("Dockerfile", "last-verified.txt"):
                shutil.copy2(self.build / name, self.config / name)
        (self.config / "bin").mkdir(exist_ok=True)
        for path in (self.build / "bin").iterdir():
            shutil.copy2(path, self.config / "bin" / path.name)
        (self.config / "last-verified.txt").write_text(
            "Verified " + self.identifier + ": Ansible 2.8.8 / Python 3.6.9; "
            "http://127.0.0.1:8080; UI " + self.manifest["uiVersion"]
            + "; evidence " + str(self.record) + "\n")

    def activate(self):
        self.check_source()
        require(self.compose.read_bytes() == self.compose_bytes, "Compose changed during deployment")
        require(run([*self.compose_cmd, "config", "--format", "json"]) == self.resolved_compose,
                "Resolved Compose settings changed during deployment")
        info = self.inspect()
        require(info["Id"] == self.old_container and info["Image"] == self.old_image,
                "The target container changed during deployment")
        self.active_check()
        self.save_configuration()
        self.announce("Stopping the idle service, taking a consistent database backup, then switching images")
        rollback_tag = "clusterforge-test-ubuntu:rollback-" + self.identifier
        self.docker_run("tag", self.old_image, rollback_tag)
        (self.record / "rollback.json").write_text(json.dumps(
            {"image": self.old_image, "tag": rollback_tag, "compose": str(self.compose)}, indent=2) + "\n")
        self.stopped = True
        self.docker_run("stop", "--time", "20", CONTAINER)
        self.active_check(offline=True)
        self.offline("database", "verify", "--db", DB, "--expected-contract", self.contract)
        backup = "/var/lib/clusterforge-test/deploy-backups/" + self.identifier
        self.docker_run("run", "--rm", "--network", "none", "--mount",
                        "type=volume,src=" + self.volumes["/var/lib/clusterforge-test"] + ",dst=/var/lib/clusterforge-test",
                        "--entrypoint", "mkdir", self.old_image, "-p", "-m", "0700", backup)
        self.offline("database", "snapshot", "--db", DB, "--target", backup + "/platform.db")
        self.offline("database", "verify", "--db", backup + "/platform.db", "--expected-contract", self.contract)
        self.docker_run("cp", CONTAINER + ":" + backup + "/platform.db", str(self.record / "platform.db"))
        self.docker_run("tag", self.new_image, IMAGE)
        run([*self.compose_cmd, "up", "-d", "--no-build", "--force-recreate", "ubuntu"],
            log=self.record / "switch.log")
        self.wait_ready(CONTAINER)
        updated = self.inspect()
        require(updated["Image"] == self.new_image, "Compose did not activate the candidate image")
        require(validate_target(updated, self.compose, self.contract) == self.volumes,
                "The updated container does not retain the original volumes")
        self.verify_image(CONTAINER, "deployed-verification.json")
        self.docker_run("exec", CONTAINER, TOOL, "database", "verify", "--db", DB,
                        "--expected-contract", self.contract)
        self.sync_configuration()
        (self.record / "result.json").write_text(json.dumps({"status": "deployed", "image": self.new_image,
            "url": "http://127.0.0.1:8080", "testsSkipped": self.options.skip_tests}, indent=2) + "\n")
        self.stopped = False

    def rollback(self):
        self.announce("Deployment failed; restoring the previous image while preserving current data")
        self.docker_run("tag", self.old_image, IMAGE)
        if self.config_changed:
            backup = self.record / "configuration"
            for name in self.config_files:
                if (backup / name).exists():
                    shutil.copy2(backup / name, self.config / name)
                else:
                    (self.config / name).unlink(missing_ok=True)
            if (backup / "bin").exists():
                for path in (backup / "bin").iterdir():
                    shutil.copy2(path, self.config / "bin" / path.name)
        run([*self.compose_cmd, "up", "-d", "--no-build", "--force-recreate", "ubuntu"],
            log=self.record / "rollback.log")
        self.wait_ready(CONTAINER)
        require(self.inspect()["Image"] == self.old_image, "Rollback image verification failed")
        (self.record / "result.json").write_text('{"status":"rolled_back","databaseRestored":false}\n')

    def cleanup(self):
        if self.candidate_created:
            try:
                self.docker_run("rm", "-f", self.candidate)
            except subprocess.CalledProcessError:
                print("Temporary container cleanup failed: " + self.candidate, file=sys.stderr)

    def execute(self):
        self.preflight()
        if self.options.check:
            return
        try:
            with tempfile.TemporaryDirectory(prefix="clusterforge-local-deploy-") as temporary:
                self.prepare(Path(temporary))
                self.build_image()
                self.tests()
                self.activate()
        except BaseException as error:
            if self.stopped:
                self.rollback()
            elif self.record.exists():
                (self.record / "result.json").write_text(json.dumps(
                    {"status": "not_deployed", "error": str(error)}, indent=2) + "\n")
            raise
        finally:
            self.cleanup()
        print("Deployed: http://127.0.0.1:8080\nEvidence and backup: " + str(self.record), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="check local runtime, database and active work without deploying")
    parser.add_argument("--skip-tests", action="store_true", help="skip Go/frontend/Ansible regression; mandatory deployment checks still run")
    options = parser.parse_args()
    def interrupted(signum, frame):
        raise RuntimeError("Deployment interrupted by signal " + str(signum))
    signal.signal(signal.SIGTERM, interrupted)
    for tool in ["docker", "git", "go", "make", "pnpm"]:
        require(shutil.which(tool), "Missing required command: " + tool)
    deployment = Deployment(options)
    require(deployment.config.is_dir(), "The local Ansible 2.8.8 environment has not been created")
    with (deployment.config / ".deploy.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError("Another local deployment is already running")
        deployment.execute()


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError, OSError) as error:
        print("error: " + str(error), file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr, file=sys.stderr)
        sys.exit(1)
