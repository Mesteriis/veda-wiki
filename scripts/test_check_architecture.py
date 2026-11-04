import contextlib
import io
import tempfile
import unittest
from pathlib import Path

import check_architecture
from check_architecture import check_imports, check_ownership, main

MODULE = 'github.com/Mesteriis/veda-wiki'


def pkg(name, imports=()):
    return {
        'ImportPath': f'{MODULE}/internal/{name}',
        'Dir': f'/repo/internal/{name}',
        'Imports': list(imports),
    }


def internal(name):
    return f'{MODULE}/internal/{name}'


def write(root, rel, text):
    path = Path(root) / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def run_main(root, packages=()):
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        code = main(root=root, packages=list(packages))
    return code, out.getvalue()


class OwnershipTests(unittest.TestCase):
    def test_flags_foreign_collection_write(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(tmp, 'internal/auth/x.go', 'package auth\n\nfunc f() {\n\tdb.Moves["k"] = v\n}\n')
            code, out = run_main(tmp)
            self.assertEqual(code, 1)
            self.assertIn('internal/auth/x.go', out)

    def test_allows_owner_write(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(tmp, 'internal/transfer/x.go', 'package transfer\n\nfunc f() {\n\tdb.Moves["k"] = v\n}\n')
            code, out = run_main(tmp)
            self.assertEqual((code, out), (0, ''))

    def test_allows_domain_and_store_writes(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(tmp, 'internal/domain/x.go', 'package domain\n\nfunc f() { db.Outbox[k] = v }\n')
            write(tmp, 'internal/store/x.go', 'package store\n\nfunc f() { delete(db.Users, k) }\n')
            self.assertEqual(run_main(tmp)[0], 0)

    def test_flags_foreign_delete(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(tmp, 'internal/importer/x.go', 'package importer\n\nfunc f() { delete(tx.DB.Outbox, k) }\n')
            code, out = run_main(tmp)
            self.assertEqual(code, 1)
            self.assertIn('internal/importer/x.go:3', out)

    def test_ignores_tests_storetest_reads_and_comments(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(tmp, 'internal/auth/x_test.go', 'package auth\n\nfunc f() { db.Moves["k"] = v }\n')
            write(tmp, 'internal/store/storetest/x.go', 'package storetest\n\nfunc f() { db.Moves["k"] = v }\n')
            write(tmp, 'internal/auth/y.go',
                  'package auth\n\n// db.Moves["k"] = v\nfunc f() {\n\tm := db.Moves["k"]\n\tif db.Moves["k"] == m {\n\t}\n}\n')
            self.assertEqual(run_main(tmp), (0, ''))

    def test_flags_whole_collection_assignment(self):
        with tempfile.TemporaryDirectory() as tmp:
            write(tmp, 'internal/httpapi/x.go', 'package httpapi\n\nfunc f() { db.Users = nil }\n')
            self.assertEqual(run_main(tmp)[0], 1)


class ImportTests(unittest.TestCase):
    def test_flags_domain_io_import(self):
        for banned in ('os', 'net', 'net/http', 'database/sql', 'os/exec'):
            with self.subTest(banned):
                found = check_imports([pkg('domain', [banned, 'strings'])], '/repo')
                self.assertEqual(len(found), 1)
                self.assertIn('internal/domain', found[0])
                self.assertIn(banned, found[0])

    def test_flags_unlisted_area_import(self):
        found = check_imports([pkg('importer', [internal('syncer')])], '/repo')
        self.assertEqual(len(found), 1)
        self.assertIn('internal/importer', found[0])

    def test_flags_area_importing_mcp_client(self):
        found = check_imports([pkg('importer', [internal('mcp/client')])], '/repo')
        self.assertEqual(len(found), 1)
        self.assertIn('internal/importer', found[0])

    def test_allows_listed_area_imports(self):
        packages = [
            pkg('syncer', [internal('importer'), internal('transfer'), internal('store')]),
            pkg('agentctx', [internal('importer')]),
        ]
        self.assertEqual(check_imports(packages, '/repo'), [])

    def test_allow_list_is_directional(self):
        self.assertEqual(len(check_imports([pkg('importer', [internal('agentctx')])], '/repo')), 1)

    def test_assembly_may_import_areas_and_leaves_may_not(self):
        areas = [internal(a) for a in sorted(check_architecture.AREAS)]
        self.assertEqual(check_imports([pkg('httpapi', areas), pkg('app', areas)], '/repo'), [])
        found = check_imports([pkg('store', [internal('auth')])], '/repo')
        self.assertEqual(len(found), 1)
        self.assertIn('internal/store', found[0])

    def test_area_may_not_import_adapter(self):
        self.assertEqual(len(check_imports([pkg('auth', [internal('httpapi')])], '/repo')), 1)

    def test_flags_unknown_internal_package(self):
        found = check_imports([pkg('util', ['strings'])], '/repo')
        self.assertEqual(len(found), 1)
        self.assertIn('internal/util: unknown internal package', found[0])
        sub = {'ImportPath': internal('store/storetest'), 'Dir': '/repo/internal/store/storetest', 'Imports': []}
        self.assertEqual(check_imports([sub], '/repo'), [])

    def test_main_exit_code_for_imports(self):
        with tempfile.TemporaryDirectory() as tmp:
            code, out = run_main(tmp, [pkg('domain', ['os'])])
            self.assertEqual(code, 1)
            self.assertIn('domain', out)


if __name__ == '__main__':
    unittest.main()
