import copy
import unittest

from prepare_windows_tray import recovery_required
from probe_windows_tray import registration_succeeded


class WindowsTrayFixtureTests(unittest.TestCase):
    def setUp(self):
        self.environment = {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted',
                            'RUNNER_OS': 'Windows', 'RUNNER_ARCH': 'ARM64'}
        self.report = {'desktop': {'session_id': 2}, 'process_machine': '0xaa64',
                       'native_machine': '0xaa64', 'emulated': False,
                       'shell_processes': [{'name': 'WWAHost.exe', 'session': '2'}],
                       'registrations': [{'stage': 'no-com-initialization',
                           'window': 'hidden-top-level', 'case': 'icon-and-callback',
                           'size': 976, 'flags': 3, 'hwnd_valid': True, 'added': False}]}

    def test_recovers_only_demonstrated_hosted_arm_oobe_failure(self):
        self.assertTrue(recovery_required(self.report, self.environment, 2))
        for key in self.environment:
            with self.subTest(missing_guard=key):
                changed = dict(self.environment)
                changed.pop(key)
                with self.assertRaises(RuntimeError):
                    recovery_required(self.report, changed, 2)

    def test_never_changes_an_already_working_shell(self):
        self.report['registrations'][0]['added'] = True
        self.assertFalse(recovery_required(self.report, {}, None))

    def test_missing_or_incomplete_probe_never_allows_recovery(self):
        for rows in [[], [{'added': False}], [{'size': 976, 'added': False}]]:
            with self.subTest(rows=rows), self.assertRaises(RuntimeError):
                recovery_required(dict(self.report, registrations=rows), self.environment, 2)

    def test_rejects_other_sessions_unknown_hosts_and_emulation(self):
        for override in [{'desktop': {'session_id': 1}},
                         {'shell_processes': [{'name': 'WWAHost.exe', 'session': '1'}]},
                         {'shell_processes': [{'name': 'unrelated.exe', 'session': '2'}]},
                         {'process_machine': '0x8664'}, {'emulated': True}]:
            with self.subTest(override=override), self.assertRaises(RuntimeError):
                recovery_required(dict(self.report, **override), self.environment, 2)
        with self.assertRaises(RuntimeError):
            recovery_required(self.report, self.environment, 0)

    def test_readiness_requires_the_production_registration_contract(self):
        self.report['registrations'][0]['added'] = True
        self.assertTrue(registration_succeeded(self.report))
        for key, invalid in [('size', 952), ('flags', 1), ('window', 'message-only'),
                             ('hwnd_valid', False), ('stage', 'after-com-sta'),
                             ('case', 'callback-only')]:
            with self.subTest(changed=key):
                changed = copy.deepcopy(self.report)
                changed['registrations'][0][key] = invalid
                self.assertFalse(registration_succeeded(changed))


if __name__ == '__main__':
    unittest.main()
