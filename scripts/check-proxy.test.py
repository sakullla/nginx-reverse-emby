"""Run with python3 scripts/check-proxy.test.py; no third-party dependencies."""
import http.server
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest

SCRIPT = Path(__file__).with_name('check-proxy.sh').resolve()


class Handler(http.server.BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.send_response(int(self.path.strip('/') or '200'))
        self.end_headers()

    def do_GET(self):
        self.send_error(500, 'The checker must never download response bodies')

    def log_message(self, *args):
        pass


class Checks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f'http://127.0.0.1:{cls.server.server_port}'

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def run_check(self, *args, env=None):
        return subprocess.run(['sh', str(SCRIPT), *args], capture_output=True,
                              text=True, env=env, timeout=25)

    def test_real_http_responses(self):
        for code, expected, text in [(200, 0, '通过'), (302, 0, '尚未验证跳转目标'),
                                     (401, 1, '访问受限'), (403, 1, '访问受限'),
                                     (404, 1, '规则匹配'), (405, 1, '不支持 HEAD'),
                                     (501, 1, '不支持 HEAD'), (502, 1, '网关不可用'),
                                     (503, 1, '网关不可用'), (504, 1, '网关不可用')]:
            with self.subTest(code=code):
                result = self.run_check('--upstream', f'{self.base}/{code}')
                self.assertEqual(result.returncode, expected, result.stderr)
                self.assertIn(text, result.stdout)
                self.assertNotIn(self.base, result.stdout)

    def test_both_targets_are_checked_after_failure(self):
        result = self.run_check('--upstream', self.base + '/502',
                                '--frontend', self.base + '/200')
        self.assertEqual(result.returncode, 1)
        self.assertIn('[需检查] 后端', result.stdout)
        self.assertIn('[通过] 入口', result.stdout)

    def test_invalid_inputs_never_echo_secrets(self):
        for args in [(), ('--upstream',), ('--unknown', 'secret'),
                     ('--upstream', 'file:///secret'),
                     ('--upstream', 'https:///secret'),
                     ('--upstream', 'https://user:secret@example.com'),
                     ('--frontend', 'https://example.com?token=secret'),
                     ('--frontend', 'https://example.com/#secret'),
                     ('--frontend', 'https://example.com/ secret')]:
            with self.subTest(args=args):
                result = self.run_check(*args)
                self.assertEqual(result.returncode, 2)
                self.assertNotIn('secret', result.stdout + result.stderr)

    def test_curl_errors_and_privacy(self):
        with tempfile.TemporaryDirectory() as directory:
            fake = Path(directory) / 'curl'
            fake.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$ARGS_FILE"\n'
                            'echo secret-url-and-token >&2\nexit "$CURL_RC"\n')
            fake.chmod(0o755)
            args_file = Path(directory) / 'args'
            env = dict(os.environ, PATH=directory + ':' + os.environ['PATH'],
                       ARGS_FILE=str(args_file))
            for code, hint in [(6, 'DNS'), (7, '连接失败'), (28, '超时'),
                               (35, 'TLS'), (60, '证书校验失败'), (99, '请求失败')]:
                with self.subTest(code=code):
                    env['CURL_RC'] = str(code)
                    result = self.run_check('--frontend', 'https://example.com', env=env)
                    self.assertEqual(result.returncode, 1)
                    self.assertIn(hint, result.stdout)
                    self.assertNotIn('secret', result.stdout + result.stderr)
                    args = args_file.read_text().splitlines()
                    self.assertEqual(args[0], '-q')
                    for required in ['--head', '--max-time', '--connect-timeout', '--noproxy']:
                        self.assertIn(required, args)
                    self.assertNotIn('--location', args)
                    self.assertNotIn('--insecure', args)

    def test_ignores_proxy_environment_and_curlrc(self):
        with tempfile.TemporaryDirectory() as directory:
            # CURL_HOME controls curl's config lookup; do not modify the user's HOME.
            Path(directory, '.curlrc').write_text('url = "http://127.0.0.1:1"\n')
            env = dict(os.environ, CURL_HOME=directory, http_proxy='http://127.0.0.1:1',
                       ALL_PROXY='http://127.0.0.1:1', no_proxy='', NO_PROXY='')
            result = self.run_check('--frontend', self.base, env=env)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_help(self):
        self.assertEqual(self.run_check('--help').returncode, 0)


if __name__ == '__main__':
    unittest.main()
