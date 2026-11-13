"""Exercise the actual deployment helper against isolated files and fake systemd.

Run in the Docker test target (Linux, root). No host services or real data used.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "deploy-release.sh"
RELEASE = "a" * 40 + "-123-1"
OLD = "#!/bin/sh\nexit 0\n# previous executable\n"
NEW = "#!/bin/sh\nexit 0\n# candidate executable\n"

MOCK = r'''#!/usr/bin/python3
import os
from pathlib import Path
import subprocess
import sys
root = Path(os.environ["FIXTURE_ROOT"])
command = Path(sys.argv[0]).name
args = sys.argv[1:]
with (root / "commands").open("a") as log:
    log.write(command + " " + " ".join(args) + "\n")
if command == "systemctl":
    assert args[-1] == "report_hosting.service", args
    if args[0] == "is-active":
        sys.exit(int((root / "stopped").exists()))
    if args[0] == "stop":
        (root / "stopped").touch()
    if args[0] in ("start", "restart"):
        (root / "stopped").unlink(missing_ok=True)
    if args[0] == "show":
        print("123")
elif command == "systemd-run":
    args = [a for a in args if not a.startswith("--")]
    sys.exit(subprocess.run(args).returncode)
elif command == "curl":
    installed = (root / "usr/local/bin/report_hosting").read_text()
    failed = os.environ.get("FAIL_HEALTH") == "1" and "candidate" in installed
    Path(args[args.index("-o") + 1]).write_text("unavailable" if failed else "access denied\n")
    print("500" if failed else "403", end="")
elif command == "sleep":
    pass
'''


class ProfileTest(unittest.TestCase):
    """Run the exact embedded profile validator without systemd (also on Windows)."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config_path = Path(self.temp.name) / "config.json"
        self.config = {"access_team_domain": "example.cloudflareaccess.com",
                       "access_aud": "synthetic-audience"}
        self.environment = {key: value for key, value in os.environ.items()
                            if not key.startswith("REPORT_HOSTING_")}
        self.validator = SCRIPT.read_text().split("<<'PY'\n", 1)[1].split("\nPY\n", 1)[0]

    def validate(self, **environment):
        self.config_path.write_text(json.dumps(self.config), encoding="utf-8")
        return subprocess.run([sys.executable, "-", str(self.config_path)],
                              input=self.validator, text=True, capture_output=True,
                              env={**self.environment, **environment}, timeout=10)

    def test_valid_production_defaults(self):
        result = self.validate()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_wrong_data_path_listener_or_protection_is_rejected(self):
        for key, value in (("data_dir", "/other"), ("listen_addr", "127.0.0.1:9999"),
                           ("production", False), ("access_check", False),
                           ("base_domain", "other.example")):
            with self.subTest(setting=key):
                self.config[key] = value
                result = self.validate()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(key, result.stderr)
                del self.config[key]

    def test_placeholder_access_settings_are_rejected(self):
        self.config["access_team_domain"] = "REPLACE.cloudflareaccess.com"
        self.assertNotEqual(self.validate().returncode, 0)

    def test_environment_overrides_are_checked(self):
        self.config["production"] = False
        self.assertEqual(self.validate(REPORT_HOSTING_PRODUCTION="true").returncode, 0)
        self.config["production"] = True
        self.assertNotEqual(self.validate(REPORT_HOSTING_PRODUCTION="false").returncode, 0)


@unittest.skipUnless(sys.platform == "linux" and getattr(os, "geteuid", lambda: -1)() == 0,
                     "Deployment rehearsal requires Linux root; use Docker test target")
class DeploymentTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = self.root / "var/lib/report_hosting-deploy"
        self.incoming = self.root / "opt/report-hosting/releases" / RELEASE
        self.binary = self.root / "usr/local/bin/report_hosting"
        self.config = self.root / "etc/report_hosting/config.json"
        self.db = self.root / "var/lib/report_hosting/db/report_hosting.sqlite"
        for directory in (self.state, self.incoming, self.binary.parent,
                          self.config.parent, self.db.parent, self.root / "mock-bin",
                          self.root / "proc/123"):
            directory.mkdir(parents=True, exist_ok=True)
        self.state.chmod(0o700)
        self.binary.write_text(OLD)
        self.binary.chmod(0o755)
        (self.root / "proc/123/exe").symlink_to(self.binary)
        self.db.write_text("synthetic database content")
        self.config.write_text(json.dumps({
            "data_dir": str(self.root / "var/lib/report_hosting"),
            "access_team_domain": "example.cloudflareaccess.com",
            "access_aud": "synthetic-audience",
        }))
        (self.config.parent / "report_hosting.env").touch()
        (self.incoming / "report_hosting").write_text(NEW)
        self.checksums()
        for name in ("systemctl", "systemd-run", "curl", "sleep"):
            executable = self.root / "mock-bin" / name
            executable.write_text(MOCK)
            executable.chmod(0o755)
        # Redirect only fixed installation paths in the test copy. Production
        # has no configurable root/PATH that a sudo caller could exploit.
        script = SCRIPT.read_text()
        for path in ("/opt/report-hosting", "/var/lib/report_hosting",
                     "/usr/local/bin/report_hosting", "/etc/report_hosting", "/proc/$pid/exe"):
            script = script.replace(path, str(self.root) + path)
        script = script.replace("export PATH=/usr/sbin:/usr/bin:/sbin:/bin",
                                f"export PATH={self.root}/mock-bin:/usr/sbin:/usr/bin:/sbin:/bin")
        self.script = self.root / "deploy.sh"
        self.script.write_text(script)
        self.environment = {key: value for key, value in os.environ.items()
                            if not key.startswith("REPORT_HOSTING_")}
        self.environment["FIXTURE_ROOT"] = str(self.root)

    def checksums(self):
        digest = hashlib.sha256((self.incoming / "report_hosting").read_bytes()).hexdigest()
        (self.incoming / "SHA256SUMS").write_text(f"{digest}  report_hosting\n")

    def deploy(self, expected=0, release=RELEASE, **environment):
        result = subprocess.run(["bash", str(self.script), release],
                                env={**self.environment, **environment},
                                capture_output=True, text=True, timeout=20)
        if expected == 0:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def assert_no_restart(self):
        log = self.root / "commands"
        self.assertNotIn("systemctl restart", log.read_text() if log.exists() else "")
        self.assertEqual(self.binary.read_text(), OLD)

    def test_success_installs_binary_without_copying_data_or_old_executable(self):
        self.deploy()
        self.assertEqual(self.binary.read_text(), NEW)
        self.assertEqual((self.state / "current-release").read_text().strip(), RELEASE)
        self.assertEqual({path.name for path in self.state.iterdir()},
                         {"deploy.lock", "current-release"})
        self.assertEqual(list(self.binary.parent.iterdir()), [self.binary])
        self.assertEqual(self.db.read_text(), "synthetic database content")

    def test_failed_health_check_fails_job_without_restoring_previous_binary(self):
        result = self.deploy(expected=1, FAIL_HEALTH="1")
        self.assertIn("Deployment failed", result.stderr)
        self.assertEqual(self.binary.read_text(), NEW)
        self.assertFalse((self.state / "current-release").exists())
        self.assertEqual(self.db.read_text(), "synthetic database content")

    def test_corrupt_artifact_fails_before_service_restart(self):
        (self.incoming / "report_hosting").write_text("corrupt")
        self.deploy(expected=1)
        self.assert_no_restart()

    def test_deployment_can_recover_an_inactive_service(self):
        (self.root / "stopped").touch()
        self.deploy()
        self.assertEqual(self.binary.read_text(), NEW)
        self.assertFalse((self.root / "stopped").exists())

    def test_bad_configuration_leaves_existing_service_running(self):
        (self.incoming / "report_hosting").write_text("#!/bin/sh\nexit 1\n")
        self.checksums()
        self.deploy(expected=1)
        self.assert_no_restart()

    def test_unsupported_data_directory_is_rejected(self):
        config = json.loads(self.config.read_text())
        config["data_dir"] = "/another/location"
        self.config.write_text(json.dumps(config))
        result = self.deploy(expected=1)
        self.assertIn("Unsupported deployment profile setting: data_dir", result.stderr)
        self.assert_no_restart()

    def test_environment_override_cannot_disable_production(self):
        self.deploy(expected=1, REPORT_HOSTING_PRODUCTION="false")
        self.assert_no_restart()

    def test_failed_deploy_can_be_retried(self):
        self.deploy(expected=1, FAIL_HEALTH="1")
        self.deploy()
        self.assertEqual((self.state / "current-release").read_text().strip(), RELEASE)

    def test_invalid_release_cannot_escape_release_directory(self):
        self.deploy(expected=1, release="../../outside")
        self.assert_no_restart()

    def test_deployments_are_serialized(self):
        import fcntl
        with (self.state / "deploy.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.deploy(expected=1)
        self.assertIn("Another report-hosting deployment", result.stderr)
        self.assert_no_restart()


if __name__ == "__main__":
    unittest.main()
