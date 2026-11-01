import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
PUBLISHER = ROOT / "scripts" / "publish-pages-artifact.sh"
INSTALLERS = ("install.sh", "install.ps1", "update.sh", "update.ps1")


class PagesPublisherTests(unittest.TestCase):
    """Runs the publisher against a throwaway gh-pages remote."""

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.environment = {name: value for name, value in os.environ.items() if not name.startswith(("MAGPIE_", "GIT_"))}
        self.environment.update({
            "GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.invalid",
            "GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.invalid",
            "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
            "MAGPIE_PAGES_WORKTREE": str(self.directory / "pages-worktree"),
        })

        self.remote = self.directory / "remote.git"
        self.git(self.directory, "init", "--quiet", "--bare", str(self.remote))

        seed = self.directory / "seed"
        self.git(self.directory, "init", "--quiet", "-b", "gh-pages", str(seed))
        self.write(seed, {"index.html": "old site", "CNAME": "magpie.tools", "stale.txt": "remove me",
                          "docs/index.html": "docs"})
        self.git(seed, "add", "-A")
        self.git(seed, "commit", "--quiet", "-m", "seed")
        self.git(seed, "push", "--quiet", str(self.remote), "gh-pages")

        # The publisher resolves its repository from its own location.
        self.repository = self.directory / "magpie"
        self.git(self.directory, "init", "--quiet", "-b", "master", str(self.repository))
        (self.repository / "scripts").mkdir()
        shutil.copy2(PUBLISHER, self.repository / "scripts" / PUBLISHER.name)
        self.git(self.repository, "remote", "add", "origin", str(self.remote))

    def git(self, cwd, *arguments):
        result = subprocess.run(["git", *arguments], cwd=cwd, env=self.environment,
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout

    def write(self, directory, files):
        for name, text in files.items():
            path = directory / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)

    def artifact(self, name, files):
        directory = self.directory / name
        self.write(directory, files)
        return directory

    def publish(self, source, target):
        return subprocess.run(["bash", str(self.repository / "scripts" / PUBLISHER.name), str(source), target, f"Publish {target}"],
                              cwd=self.directory, env=self.environment, capture_output=True, text=True, timeout=60)

    def published(self):
        names = self.git(self.directory, "--git-dir", str(self.remote), "ls-tree", "-r", "--name-only", "gh-pages")
        return {name: self.git(self.directory, "--git-dir", str(self.remote), "show", f"gh-pages:{name}")
                for name in names.split()}

    def installers(self, version):
        return self.artifact(f"installers-{version}", {name: f"{name} {version}" for name in INSTALLERS})

    def test_installers_are_added_without_touching_the_site(self):
        result = self.publish(self.installers("v1"), "installers")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        files = self.published()
        for name in INSTALLERS:
            self.assertEqual(files[name], f"{name} v1")
        self.assertEqual(files["index.html"], "old site")
        self.assertEqual(files["docs/index.html"], "docs")
        self.assertIn("stale.txt", files)

    def test_installer_artifact_must_contain_every_script(self):
        source = self.artifact("partial", {"install.sh": "only one"})
        result = self.publish(source, "installers")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("missing install.ps1", result.stderr)
        self.assertNotIn("install.sh", self.published())

    def test_website_deploy_keeps_installers_and_docs(self):
        self.assertEqual(self.publish(self.installers("v1"), "installers").returncode, 0)
        site = self.artifact("site", {"index.html": "new site", "CNAME": "magpie.tools"})
        result = self.publish(site, "root")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        files = self.published()
        self.assertEqual(files["index.html"], "new site")
        self.assertNotIn("stale.txt", files)
        self.assertEqual(files["docs/index.html"], "docs")
        for name in INSTALLERS:
            self.assertEqual(files[name], f"{name} v1")

    def test_republishing_installers_replaces_them(self):
        self.assertEqual(self.publish(self.installers("v1"), "installers").returncode, 0)
        result = self.publish(self.installers("v2"), "installers")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.published()["install.sh"], "install.sh v2")


if __name__ == "__main__":
    unittest.main()
