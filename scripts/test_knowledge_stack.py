"""The knowledge dev stack keeps its secrets private and never prints them."""
import contextlib
import importlib.util
import io
from pathlib import Path
import stat
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('knowledge_stack', Path(__file__).with_name('knowledge_stack.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

ROOT = Path(__file__).resolve().parent.parent


class KnowledgeStackTests(unittest.TestCase):
    def patched(self, tmp):
        secrets = Path(tmp) / '.secrets'
        return mock.patch.multiple(module, SECRETS=secrets, NEO4J_PASSWORD=secrets / 'neo4j_password',
                                   NEO4J_AUTH=secrets / 'neo4j_auth', EXTRACTOR_KEY=secrets / 'extractor.key')

    def test_secrets_are_private_stable_and_never_printed(self):
        with tempfile.TemporaryDirectory() as tmp, self.patched(tmp):
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                self.assertEqual(module.main(['secrets']), 0)
            self.assertEqual(stat.S_IMODE(module.SECRETS.stat().st_mode), 0o700)
            values = {}
            for path in (module.NEO4J_PASSWORD, module.NEO4J_AUTH, module.EXTRACTOR_KEY):
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
                values[path] = path.read_text().strip()
                self.assertGreaterEqual(len(values[path]), 32)
                self.assertNotIn(values[path], out.getvalue())
            password = values[module.NEO4J_PASSWORD]
            self.assertEqual(values[module.NEO4J_AUTH], f'neo4j/{password}')
            self.assertNotIn(password, out.getvalue())
            module.ensure_secrets()
            for path, value in values.items():
                self.assertEqual(path.read_text().strip(), value)

    def test_secret_dir_is_git_ignored_and_ports_are_loopback(self):
        ignore = (ROOT / '.gitignore').read_text().splitlines()
        self.assertIn('deploy/knowledge/.secrets/', ignore)
        compose = (ROOT / 'deploy' / 'knowledge' / 'compose.yaml').read_text()
        ports = [line.strip() for line in compose.splitlines() if line.strip().startswith('- "') and ':' in line and 'PORT' in line]
        self.assertEqual(len(ports), 2)
        for line in ports:
            self.assertTrue(line.startswith('- "127.0.0.1:'), line)
        # Neo4j and the extractor sit only on the internal backend network.
        self.assertIn('  backend:\n    internal: true', compose)
        self.assertEqual(compose.count('networks: [backend]\n'), 2)

    def test_compose_uses_the_selected_project_and_overrides(self):
        with mock.patch.object(module.subprocess, 'run') as run, \
                mock.patch.multiple(module, PROJECT='veda-knowledge-smoke', OVERRIDES=('/tmp/override.yaml',)):
            module.compose('ps')
        cmd = run.call_args.args[0]
        self.assertEqual(cmd[:4], ['docker', 'compose', '-f', str(module.COMPOSE)])
        self.assertEqual(cmd[4:], ['-f', '/tmp/override.yaml', '-p', 'veda-knowledge-smoke', 'ps'])
        with mock.patch.object(module.subprocess, 'run') as run:
            module.compose('ps')
        self.assertEqual(run.call_args.args[0], ['docker', 'compose', '-f', str(module.COMPOSE), 'ps'])


if __name__ == '__main__':
    unittest.main()
