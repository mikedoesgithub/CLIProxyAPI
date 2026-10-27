#!/usr/bin/env python3
"""Exercise the production sync script against isolated local Git remotes."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('sync-upstream.sh').resolve()


def git(cwd, *args):
    result = subprocess.run(['git', '-C', str(cwd), *args], capture_output=True, text=True)
    if result.returncode:
        raise AssertionError(result.stderr)
    return result.stdout.strip()


class SyncTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='prox-sync-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.upstream = self.root / 'upstream'
        self.origin = self.root / 'fork.git'
        self.checkout = self.root / 'checkout'
        git(self.root, 'init', '-b', 'main', str(self.upstream))
        self.identity(self.upstream)
        self.commit(self.upstream, 'shared.txt', 'base\n')
        git(self.root, 'clone', '--bare', str(self.upstream), str(self.origin))
        git(self.root, 'clone', str(self.origin), str(self.checkout))
        self.identity(self.checkout)
        self.before = git(self.checkout, 'rev-parse', 'HEAD')

    def identity(self, repo):
        git(repo, 'config', 'user.name', 'Sync fixture')
        git(repo, 'config', 'user.email', 'sync-fixture@example.invalid')

    def commit(self, repo, name, content):
        (repo / name).write_text(content)
        git(repo, 'add', name)
        git(repo, 'commit', '-m', 'fixture change')

    def sync(self, merge=True):
        env = os.environ.copy()
        env.update(UPSTREAM_REMOTE_URL=str(self.upstream), TARGET_BRANCH='main',
                   SYNC_MERGE=str(merge).lower(), GITHUB_OUTPUT=str(self.root / 'output'),
                   GITHUB_STEP_SUMMARY=str(self.root / 'summary'))
        result = subprocess.run(['bash', str(SCRIPT)], cwd=self.checkout, env=env,
                                capture_output=True, text=True)
        self.output = (self.root / 'output').read_text()
        return result

    def remote_head(self):
        return git(self.origin, 'rev-parse', 'main')

    def assert_clean(self):
        self.assertEqual(git(self.checkout, 'status', '--porcelain'), '')
        result = subprocess.run(['git', '-C', str(self.checkout), 'rev-parse', '-q',
                                 '--verify', 'MERGE_HEAD'], capture_output=True)
        self.assertNotEqual(result.returncode, 0)

    def test_no_updates_does_not_push(self):
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('status=up-to-date', self.output)
        self.assertEqual(self.remote_head(), self.before)
        self.assert_clean()

    def test_check_only_aborts_clean_merge_without_writing(self):
        self.commit(self.upstream, 'upstream.txt', 'new upstream\n')
        result = self.sync(merge=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('status=mergeable', self.output)
        self.assertEqual(self.remote_head(), self.before)
        self.assertEqual(git(self.checkout, 'rev-parse', 'HEAD'), self.before)
        self.assertFalse((self.checkout / 'upstream.txt').exists())
        self.assert_clean()

    def test_clean_divergence_merges_and_preserves_customizations(self):
        self.commit(self.checkout, 'prox.txt', 'our custom UI\n')
        git(self.checkout, 'push', 'origin', 'main')
        custom = self.remote_head()
        self.commit(self.upstream, 'upstream.txt', 'upstream fix\n')
        incoming = git(self.upstream, 'rev-parse', 'HEAD')
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('status=merged', self.output)
        self.assertIn('changed=true', self.output)
        self.assertEqual((self.checkout / 'prox.txt').read_text(), 'our custom UI\n')
        self.assertEqual((self.checkout / 'upstream.txt').read_text(), 'upstream fix\n')
        self.assertEqual(git(self.checkout, 'rev-parse', 'HEAD^1'), custom)
        self.assertEqual(git(self.checkout, 'rev-parse', 'HEAD^2'), incoming)
        self.assertEqual(self.remote_head(), git(self.checkout, 'rev-parse', 'HEAD'))
        self.assert_clean()

    def test_conflict_never_changes_fork(self):
        self.commit(self.checkout, 'shared.txt', 'prox version\n')
        git(self.checkout, 'push', 'origin', 'main')
        custom = self.remote_head()
        self.commit(self.upstream, 'shared.txt', 'upstream version\n')
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('status=conflicts', self.output)
        self.assertIn('shared.txt', (self.root / 'summary').read_text())
        self.assertEqual(self.remote_head(), custom)
        self.assertEqual((self.checkout / 'shared.txt').read_text(), 'prox version\n')
        self.assert_clean()

    def test_stale_checkout_rejects_concurrent_fork_change(self):
        other = self.root / 'other'
        git(self.root, 'clone', str(self.origin), str(other))
        self.identity(other)
        self.commit(other, 'human.txt', 'concurrent change\n')
        git(other, 'push', 'origin', 'main')
        remote = self.remote_head()
        self.commit(self.upstream, 'upstream.txt', 'upstream\n')
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.remote_head(), remote)
        self.assertEqual(git(self.checkout, 'rev-parse', 'HEAD'), self.before)
        self.assert_clean()

    def test_rejected_push_does_not_report_success(self):
        self.commit(self.upstream, 'upstream.txt', 'upstream\n')
        hook = self.origin / 'hooks' / 'pre-receive'
        hook.write_text('#!/bin/sh\nexit 1\n')
        hook.chmod(0o755)
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('status=merged', self.output)
        self.assertNotIn('changed=true', self.output)
        self.assertEqual(self.remote_head(), self.before)
        self.assert_clean()


if __name__ == '__main__':
    unittest.main()
