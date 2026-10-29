import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import tempfile
import socket
import ssl
import subprocess
import threading
import time
import unittest
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[2] / 'skills/tailnet-preview/scripts/tailnet_preview.py'
spec = importlib.util.spec_from_file_location('tailnet_preview', SCRIPT)
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)

FAKE = r'''#!/usr/bin/env python3
import json, os, pathlib, select, socket, sys, time
root = pathlib.Path(os.environ['FAKE_ROOT'])
args = sys.argv[1:]
with (root/'argv').open('a') as f: f.write(json.dumps(args, ensure_ascii=False)+'\n')
config = json.loads((root/'config').read_text())
if args == ['status', '--json']:
    status = {'BackendState':'Running','CurrentTailnet':{'MagicDNSSuffix':'example.ts.net'},'Self':{'ID':'n123','DNSName':'dev.example.ts.net.','TailscaleIPs':['fd7a:115c:a1e0::1','100.64.0.1']},'Peer':{'n456':{'DNSName':'home.example.ts.net.','TailscaleIPs':['fd7a:115c:a1e0::2','100.64.0.2']}}}
    print((root/'status').read_text() if (root/'status').exists() else json.dumps(status))
elif args[:1] == ['nc']:
    (root/'nc-pid').write_text(str(os.getpid()))
    if os.environ.get('NC_STALL'):
        time.sleep(60)
    connection = socket.create_connection(('127.0.0.1', int(os.environ['NC_PORT'])))
    while True:
        readable, _, _ = select.select([sys.stdin.buffer, connection], [], [])
        for source in readable:
            data = os.read(source.fileno(), 65536)
            if not data:
                sys.exit(0)
            if source is connection:
                os.write(sys.stdout.fileno(), data)
            else:
                connection.sendall(data)
elif args == ['version']:
    print('1.90.0')
elif args == ['debug','prefs']:
    if os.environ.get('FAIL_PREFS'):
        print('prefs unavailable', file=sys.stderr); sys.exit(1)
    print(json.dumps({'OperatorUser':os.environ.get('FAKE_OPERATOR','')}))
elif args == ['serve','status','--json']:
    print(json.dumps(config))
else:
    if os.environ.get('DENY_SERVE'):
        print('access denied', file=sys.stderr); sys.exit(1)
    config = config or {}
    port = next(x.split('=',1)[1] for x in args if x.startswith('--https='))
    path = next(x.split('=',1)[1] for x in args if x.startswith('--set-path='))
    hp = 'dev.example.ts.net:'+port
    if os.environ.get('FAIL_CARD') and args[-1].startswith('text:'):
        print('fixture card failure',file=sys.stderr); sys.exit(1)
    if args[-1] == 'off':
        del config['Web'][hp]['Handlers'][path]
        if not config['Web'][hp]['Handlers']:
            del config['Web'][hp]; del config['TCP'][port]
    else:
        config.setdefault('TCP',{})[port] = {'HTTPS':True}
        config.setdefault('Web',{}).setdefault(hp,{'Handlers':{}})['Handlers'][path] = ({'Text':args[-1][5:]} if args[-1].startswith('text:') else {'Proxy':args[-1]})
    (root/'config').write_text(json.dumps(config))
'''


class HelperTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='preview fixtures ')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        fake = self.root / 'tailscale'
        fake.write_text(FAKE)
        fake.chmod(0o700)
        (self.root / 'config').write_text('{}')
        self.env = mock.patch.dict(os.environ, {'PATH': str(self.root) + os.pathsep + os.environ['PATH'],
                                               'FAKE_ROOT': str(self.root), 'XDG_STATE_HOME': str(self.root / 'state'),
                                               'TAILNET_PREVIEW_PORTS': '8443-8445', 'TAILHOME_URL': '', 'TAILSCALE': ''})
        self.env.start()
        self.addCleanup(self.env.stop)
        helper.tailnet_status.cache_clear()
        self.addCleanup(helper.tailnet_status.cache_clear)
        self.card = self.root / 'card.json'
        self.card.write_text(json.dumps({'title': "A · Owner's hero", 'description': 'Inspect café', 'set': 'Landing', 'version': 'A'}))
        self.network = mock.patch.object(helper, 'request', side_effect=self.respond)
        self.network.start()
        self.addCleanup(self.network.stop)

    def respond(self, url, method='GET', limit=helper.MAX_BODY, prefix=False, timeout=helper.TIMEOUT):
        if url.endswith(helper.CARD_PATH):
            config = json.loads((self.root / 'config').read_text())
            hp = __import__('urllib.parse', fromlist=['urlsplit']).urlsplit(url).netloc
            if ':' not in hp:
                hp += ':443'
            return 200, config['Web'][hp]['Handlers'][helper.CARD_PATH]['Text'].encode()
        return 200, b'expected screen'

    def call(self, *args):
        out = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
            code = helper.main(list(args))
        return code, json.loads(out.getvalue())

    def publish(self, preview_id='a', *extra):
        return self.call('publish', '--id', preview_id, '--upstream', 'http://127.0.0.1:3000',
                         '--card', str(self.card), '--expect-text', 'expected screen', *extra)

    def config(self):
        return json.loads((self.root / 'config').read_text())

    def save_config(self, config):
        (self.root / 'config').write_text(json.dumps(config))

    def commands(self):
        return [json.loads(line) for line in (self.root / 'argv').read_text().splitlines()]

    def off(self, port, path):
        return [str(self.root / 'tailscale'), 'serve', '--https=' + str(port),
                '--set-path=' + path, 'off']

    def catalog_entry(self, record):
        entry = dict(json.loads(record['card']), url=record['url'])
        entry['label'] = entry.pop('title')
        return entry


    def test_preflight_operator_permissions_and_read_only_ports(self):
        self.save_config({'TCP': {'8443': {'HTTPS': True}}})
        for operator, expected in [('reviewer', 'allowed'), ('', 'denied_likely')]:
            with self.subTest(operator=operator), \
                    mock.patch.object(helper.sys, 'platform', 'linux'), \
                    mock.patch.object(helper.os, 'geteuid', return_value=1000), \
                    mock.patch.object(helper.getpass, 'getuser', return_value='reviewer'), \
                    mock.patch.dict(os.environ, {'FAKE_OPERATOR': operator}):
                code, out = self.call('preflight', '--ports', '8443-8445')
            self.assertEqual(code, 0)
            self.assertEqual(out['serve_write'], expected)
            self.assertEqual(out['free_ports'], [8444, 8445])
            self.assertEqual(out['machine_name'], 'dev')
            self.assertEqual(out['cli_version'], '1.90.0')
            if expected == 'denied_likely':
                self.assertIn('sudo tailscale set --operator=$USER', out['serve_write_fix'])
        self.assertFalse((self.root / 'state').exists())
        self.assertTrue(all(command in [['version'], ['status', '--json'],
                                       ['debug', 'prefs'], ['serve', 'status', '--json']]
                            for command in self.commands()))

    def test_preflight_root_prefs_failure_and_macos(self):
        for platform, uid, failed, expected in [
                ('linux', 0, '', 'allowed'), ('linux', 1000, '1', 'unknown'),
                ('darwin', 1000, '', 'unknown')]:
            with self.subTest(platform=platform, uid=uid, failed=failed), \
                    mock.patch.object(helper.sys, 'platform', platform), \
                    mock.patch.object(helper.os, 'geteuid', return_value=uid), \
                    mock.patch.dict(os.environ, {'FAIL_PREFS': failed,
                                                'TAILNET_PREVIEW_PORTS': ''}):
                code, out = self.call('preflight')
            self.assertEqual(code, 0)
            self.assertEqual(out['serve_write'], expected)
            self.assertIsNone(out['free_ports'])

    def test_preflight_hard_blockers(self):
        for status in [
                {'BackendState': 'NeedsLogin'},
                {'BackendState': 'Running', 'Self': {'ID': 'n123', 'DNSName': 'dev.'}}]:
            (self.root / 'status').write_text(json.dumps(status))
            code, out = self.call('preflight')
            self.assertEqual(code, 1)
            self.assertTrue(out['blockers'])
        with mock.patch.object(helper.shutil, 'which', return_value=None):
            code, out = self.call('preflight')
        self.assertEqual(code, 1)
        self.assertIsNone(out['cli_path'])

    def test_denied_serve_remediation_without_stale_recovery_claim(self):
        with mock.patch.dict(os.environ, {'DENY_SERVE': '1'}):
            code, out = self.publish()
        self.assertEqual(code, 1)
        self.assertIn('sudo tailscale set --operator=$USER', out['warnings'][0])
        self.assertFalse(helper.Store().path('a').exists())
        self.assertNotIn('recoverable ownership state', str(helper.PublicationFailure(out)))

    def test_other_hosts_use_urllib(self):
        self.network.stop()
        response = mock.MagicMock(status=200)
        response.__enter__.return_value = response
        response.read.return_value = b'ok'
        with mock.patch.object(helper.OPENER, 'open', return_value=response) as opened:
            self.assertEqual(helper.request('https://example.com/'), (200, b'ok'))
        self.assertEqual(opened.call_count, 1)
        self.assertFalse(any(command[:1] == ['nc'] for command in self.commands()))

    def test_selected_executable_for_real_and_dry_run_lifecycle(self):
        selected = self.root / 'Tailscale app executable'
        selected.write_text(FAKE)
        selected.chmod(0o700)
        with mock.patch.dict(os.environ, {'TAILSCALE': str(selected)}), \
                mock.patch.object(helper.subprocess, 'run', wraps=helper.subprocess.run) as run:
            code, planned = self.publish('planned', '--dry-run')
            self.assertEqual(code, 0)
            self.assertTrue(all(command[0] == str(selected) for command in planned['commands']))
            self.assertFalse(any('--bg' in command for command in self.commands()))
            self.assertEqual(self.publish()[0], 0)
            before = self.config()
            record_before = helper.Store().load('a')
            code, cleanup = self.call('cleanup', '--id', 'a', '--dry-run')
            self.assertEqual(code, 0)
            self.assertTrue(all(command[0] == str(selected) for command in cleanup['commands']))
            self.assertEqual(self.config(), before)
            self.assertEqual(helper.Store().load('a'), record_before)
            self.assertEqual(self.call('status', '--id', 'a')[0], 0)
            self.assertEqual(self.call('cleanup', '--id', 'a')[0], 0)
            self.assertTrue(all(call.args[0][0] == str(selected) for call in run.call_args_list))

    def test_missing_pool_has_actionable_error_without_cli_or_mutation(self):
        with mock.patch.dict(os.environ, {'TAILNET_PREVIEW_PORTS': ''}), \
                mock.patch.object(helper, 'run') as run:
            code, out = self.publish()
        self.assertEqual(code, 1)
        for text in ['No approved HTTPS ports', '--ports', 'TAILNET_PREVIEW_PORTS', '443', 'explicit approval']:
            self.assertIn(text, out['error'])
        run.assert_not_called()
        self.assertEqual(self.config(), {})

    def test_help_and_local_list_do_not_resolve_cli(self):
        with mock.patch.object(helper, 'cli_argv', side_effect=AssertionError('CLI resolution')):
            self.assertEqual(self.call('list'), (0, {'previews': []}))
            for command in ['publish', 'status', 'check', 'cleanup', 'list']:
                output = io.StringIO()
                with contextlib.redirect_stdout(output), self.assertRaises(SystemExit) as exit:
                    helper.main([command, '--help'])
                self.assertEqual(exit.exception.code, 0)
                text = ' '.join(output.getvalue().split())
                if command != 'list':
                    self.assertIn('TAILSCALE_BE_CLI=1', text)
                if command in ['publish', 'status', 'check']:
                    self.assertIn('Optional trusted full HTTPS tailhome URL', text)
                    self.assertIn('TAILHOME_URL', text)
                if command == 'publish':
                    for phrase in ['TAILNET_PREVIEW_PORTS', 'No built-in pool', '443 needs explicit approval',
                                   'Does not start the app', 'still reads local and Tailscale state']:
                        self.assertIn(phrase, text)
                if command == 'cleanup':
                    self.assertIn('without changing Serve or ownership records', text)

    def test_independent_account_roots_and_hosts_reuse_id_and_set(self):
        self.assertEqual(self.publish('a')[0], 0)
        original = helper.Store().path('a').read_bytes()
        before = self.config()
        with mock.patch.dict(os.environ, {'XDG_STATE_HOME': str(self.root / 'second-account')}):
            self.assertEqual(self.call('list')[1]['previews'], [])
            self.assertEqual(self.call('cleanup', '--id', 'a')[0], 1)
            self.assertEqual(self.config(), before)
            with mock.patch.object(helper, 'identity', return_value={'node': 'second-node', 'fqdn': 'other.example.ts.net'}):
                code, plan = self.publish('a', '--ports', '8444', '--dry-run')
                self.assertEqual(code, 0)
                self.assertEqual(plan['url'], 'https://other.example.ts.net:8444/')
                record = json.loads(original)
                record.update(node='second-node', fqdn='other.example.ts.net')
                helper.Store().save(record)
                self.assertEqual(self.call('list', '--set', 'Landing')[1]['previews'][0]['id'], 'a')
            code, out = self.call('cleanup', '--id', 'a')
            self.assertEqual(code, 1)
            self.assertIn('node/FQDN differs', out['warnings'][0])
        self.assertEqual(helper.Store().path('a').read_bytes(), original)
        self.assertEqual(self.config(), before)

    def test_lowest_free_and_independent_previews(self):
        permanent = {'TCP': {'443': {'HTTPS': True}, '8443': {'HTTPS': True}},
                     'Web': {'dev.example.ts.net:443': {'Handlers': {'/': {'Proxy': 'http://127.0.0.1:9000'}}}}}
        self.save_config(permanent)
        code, out = self.publish()
        self.assertEqual(code, 0)
        self.assertEqual(out['url'], 'https://dev.example.ts.net:8444/')
        self.assertTrue(out['published'] and out['app_verified'] and out['card_verified'])
        self.assertIsNone(out['listed'])
        self.assertEqual(self.publish('b')[0], 0)
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 0)
        self.assertIn('dev.example.ts.net:8445', self.config()['Web'])
        self.assertEqual(self.config()['Web']['dev.example.ts.net:443'], permanent['Web']['dev.example.ts.net:443'])

    def test_argv_preserves_apostrophe_unicode_and_no_shell(self):
        self.assertEqual(self.publish()[0], 0)
        text = [cmd[-1] for cmd in self.commands() if cmd[-1].startswith('text:')][0]
        self.assertEqual(json.loads(text[5:])['title'], "A · Owner's hero")
        self.assertTrue(json.loads(text[5:])['preview'])
        self.assertNotIn('\\"', text)

    def test_refusal_cases(self):
        cases = [
            {'TCP': {'8443': {'HTTPS': True}, '8444': {'HTTPS': True}, '8445': {'HTTPS': True}}},
            {'AllowFunnel': {'dev.example.ts.net:8443': True, 'dev.example.ts.net:8444': True, 'dev.example.ts.net:8445': True}},
            {'Foreground': {'session': {'TCP': {'8443': {'HTTPS': True}, '8444': {'HTTPS': True}, '8445': {'HTTPS': True}}}}},
            {'Services': {'svc:app': {'TCP': {'8443': {'HTTPS': True}, '8444': {'HTTPS': True}, '8445': {'HTTPS': True}}}}},
            {'NewUnknownField': {}}, {'Web': {'broken': {'Handlers': {}}}}, {'TCP': {'8443': {'Future': True}}},
            {'Services': {'svc:app': {'Tun': True}}},
        ]
        for config in cases:
            with self.subTest(config=config):
                self.save_config(config)
                code, _ = self.publish()
                self.assertEqual(code, 1)
                self.assertEqual(self.config(), config)
        self.assertFalse(any('--bg' in cmd for cmd in self.commands()))

    def test_partial_failure_compensates_root(self):
        with mock.patch.dict(os.environ, {'FAIL_CARD': '1'}):
            code, out = self.publish()
        self.assertEqual(code, 1)
        self.assertFalse(out['published'])
        self.assertEqual(self.config().get('Web'), {})
        removals = [cmd for cmd in self.commands() if cmd[-1] == 'off']
        self.assertEqual(len(removals), 1)
        self.assertIn('--set-path=/', removals[0])

    def test_failed_publish_with_home_reports_unconfirmed_listing(self):
        with mock.patch.dict(os.environ, {'FAIL_CARD': '1'}), \
                mock.patch.object(helper, 'request', side_effect=self.respond) as request:
            code, out = self.publish('a', '--home', 'https://home.example.ts.net')
        self.assertEqual(code, 1)
        self.assertIs(out['listed'], False)
        self.assertFalse(out['published'])
        self.assertFalse(any(call.args[0].startswith('https://home.example.ts.net')
                             for call in request.call_args_list))

    def test_cleanup_preserves_foreign_path_and_changed_root(self):
        self.assertEqual(self.publish()[0], 0)
        config = self.config()
        handlers = config['Web']['dev.example.ts.net:8443']['Handlers']
        handlers['/foreign'] = {'Text': 'human'}
        handlers['/'] = {'Proxy': 'http://127.0.0.1:9999'}
        self.save_config(config)
        code, out = self.call('cleanup', '--id', 'a')
        self.assertEqual(code, 1)
        self.assertTrue(out['warnings'])
        handlers = self.config()['Web']['dev.example.ts.net:8443']['Handlers']
        self.assertEqual(handlers['/foreign'], {'Text': 'human'})
        self.assertEqual(handlers['/']['Proxy'], 'http://127.0.0.1:9999')
        self.assertNotIn(helper.CARD_PATH, handlers)
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 1)

    def test_changed_card_does_not_prevent_exact_root_cleanup(self):
        self.assertEqual(self.publish()[0], 0)
        config = self.config()
        config['Web']['dev.example.ts.net:8443']['Handlers'][helper.CARD_PATH] = {'Text': 'human'}
        self.save_config(config)
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 1)
        self.assertEqual(self.config()['Web']['dev.example.ts.net:8443']['Handlers'], {helper.CARD_PATH: {'Text': 'human'}})

    def test_dry_run_and_missing_state_never_mutate(self):
        code, out = self.publish('dry', '--dry-run')
        self.assertEqual(code, 0)
        self.assertEqual(len(out['commands']), 2)
        self.assertEqual(self.config(), {})
        self.assertEqual(self.call('cleanup', '--id', 'dry')[0], 1)
        self.assertEqual(self.call('list')[1], {'previews': []})

    def test_private_state_and_repeated_cleanup(self):
        self.assertEqual(self.publish()[0], 0)
        path = self.root / 'state/tailnet-preview/a.json'
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(path.parent.stat().st_mode), 0o700)
        self.assertEqual(self.call('status', '--id', 'a')[0], 0)
        self.assertEqual(self.publish()[0], 1)
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 0)
        before = self.config()
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 1)
        self.assertEqual(self.config(), before)

    def test_app_failure_never_reports_success(self):
        self.network.stop()
        def response(url, **kwargs):
            if url.startswith('https:'):
                raise helper.Failure('HTTP 502')
            return self.respond(url, **kwargs)
        with mock.patch.object(helper, 'request', side_effect=response):
            code, out = self.publish()
        self.assertEqual(code, 1)
        self.assertIn('stage=HTTPS card', out['error'])
        self.assertIn('error=HTTP 502', out['warnings'][0])
        self.assertTrue(out['compensation_completed'])
        self.assertFalse(out['ownership_record_remains'])
        self.assertEqual(helper.config_ports(self.config()), set())
        self.assertFalse(helper.Store().path('a').exists())

    def test_first_https_retries_transport_failure_before_compensation(self):
        card_attempts = 0
        def response(url, **kwargs):
            nonlocal card_attempts
            if url.endswith(helper.CARD_PATH):
                card_attempts += 1
                if card_attempts == 1:
                    raise helper.Failure('Request failed at ' + url + ': timed out')
            return self.respond(url, **kwargs)
        with mock.patch.object(helper, 'request', side_effect=response) as request, \
                mock.patch.object(helper, 'time', wraps=helper.time) as timer:
            timer.sleep.return_value = None
            code, out = self.publish()
        self.assertEqual(code, 0)
        self.assertTrue(out['published'] and out['app_verified'] and out['card_verified'])
        self.assertEqual(card_attempts, 2)
        timer.sleep.assert_called_once_with(3)
        self.assertEqual(sum(call.args[0] == out['url'] for call in request.call_args_list), 1)
        self.assertFalse(any(cmd[-1] == 'off' for cmd in self.commands()))

    def test_first_https_transport_retry_is_bounded_before_compensation(self):
        clock = [0]
        def response(url, **kwargs):
            if url.startswith('https://'):
                clock[0] += kwargs['timeout']
                raise helper.Failure('Request failed at ' + url + ': timed out')
            return self.respond(url, **kwargs)
        def sleep(seconds):
            clock[0] += seconds
        with mock.patch.object(helper, 'request', side_effect=response), \
                mock.patch.object(helper, 'time', wraps=helper.time) as timer:
            timer.monotonic.side_effect = lambda: clock[0]
            timer.sleep.side_effect = sleep
            code, out = self.publish()
        self.assertEqual(code, 1)
        self.assertFalse(out['published'])
        self.assertEqual(clock[0], 90)
        self.assertEqual(self.config()['Web'], {})
        self.assertTrue(any(cmd[-1] == 'off' for cmd in self.commands()))

    def test_first_https_does_not_retry_http_failure(self):
        def response(url, **kwargs):
            if url.startswith('https://'):
                raise helper.Failure('HTTP 502 at ' + url)
            return self.respond(url, **kwargs)
        with mock.patch.object(helper, 'request', side_effect=response), \
                mock.patch.object(helper, 'time', wraps=helper.time) as timer:
            timer.sleep.return_value = None
            code, out = self.publish()
        self.assertEqual(code, 1)
        self.assertFalse(out['published'])
        timer.sleep.assert_not_called()

    def test_home_refresh_timeout_still_checks_both_catalogs(self):
        self.assertEqual(self.publish()[0], 0)
        record = helper.Store().load('a')
        entry = self.catalog_entry(record)
        def response(url, **kwargs):
            if url.endswith('/refresh'):
                self.assertEqual(kwargs['method'], 'POST')
                raise helper.Failure('Request failed at ' + url + ': timed out')
            if url.endswith('/api/services.json'):
                return 200, json.dumps({'updated': stamp}).encode()
            if url.endswith('/api/list.json'):
                return 200, json.dumps([entry]).encode()
            self.fail('Unexpected request: ' + url)
        for stamp, listed in [('2099-01-01T00:00:00Z', True), ('2000-01-01T00:00:00Z', False)]:
            with self.subTest(stamp=stamp):
                out = helper.result(record)
                with mock.patch.object(helper, 'request', side_effect=response) as request, \
                        mock.patch.object(helper.time, 'sleep'):
                    helper.home_confirm('https://home.example.ts.net', record, out)
                self.assertEqual(out['listed'], listed)
                urls = [call.args[0] for call in request.call_args_list]
                self.assertIn('https://home.example.ts.net/api/services.json', urls)
                self.assertIn('https://home.example.ts.net/api/list.json', urls)
                self.assertEqual(bool(out['warnings']), not listed)

    def test_home_freshness_and_metadata_both_required(self):
        self.assertEqual(self.publish()[0], 0)
        record = helper.Store().load('a')
        out = helper.result(record)
        entry = {'url': record['url'], 'label': "A · Owner's hero", 'description': 'Inspect café', 'set': 'Landing', 'version': 'A'}
        def response(url, **kwargs):
            if url.endswith('/refresh'):
                raise helper.Failure('HTTP 303 at fixture; redirect not followed')
            if url.endswith('services.json'):
                return 200, json.dumps({'updated': '2000-01-01T00:00:00Z'}).encode()
            return 200, json.dumps([entry]).encode()
        with mock.patch.object(helper, 'request', side_effect=response), mock.patch.object(helper.time, 'sleep') as sleep:
            helper.home_confirm('https://home.example.ts.net', record, out)
        self.assertFalse(out['listed'])
        self.assertEqual(sleep.call_args_list, [mock.call(11), mock.call(11)])
        self.assertIn('Published; home listing not confirmed.', out['warnings'][0])

    def test_concurrent_cooperating_publishers(self):
        import multiprocessing
        ctx = multiprocessing.get_context('fork')
        queue = ctx.Queue()
        def worker(name):
            queue.put(self.publish(name))
        workers = [ctx.Process(target=worker, args=(name,)) for name in ('a', 'b')]
        try:
            for process in workers:
                process.start()
            for process in workers:
                process.join(20)
            self.assertEqual([process.exitcode for process in workers], [0, 0])
            results = [queue.get(timeout=2) for _ in workers]
            self.assertEqual([code for code, _ in results], [0, 0])
            self.assertEqual(len({out['url'] for _, out in results}), 2)
        finally:
            for process in workers:
                if process.is_alive():
                    process.terminate()
                    process.join()
            queue.close()
            queue.join_thread()

    def test_recover_crash_between_mounts(self):
        self.assertEqual(self.publish()[0], 0)
        store = helper.Store()
        record = store.load('a')
        record['phase'] = 'writing-root'
        store.save(record)
        config = self.config()
        del config['Web']['dev.example.ts.net:8443']['Handlers'][helper.CARD_PATH]
        self.save_config(config)
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 0)
        self.assertEqual(self.config()['Web'], {})
        self.assertEqual(self.call('cleanup', '--id', 'a')[0], 1)

    def test_fresh_home_requires_exact_fields(self):
        self.assertEqual(self.publish()[0], 0)
        record = helper.Store().load('a')
        entry = {'url': record['url'], 'label': "A · Owner's hero", 'description': 'Inspect café', 'set': 'Landing', 'version': 'A'}
        stamp = '2099-01-01T00:00:00.76051149Z'
        def response(url, **kwargs):
            if url.endswith('/refresh'):
                raise helper.Failure('HTTP 303 at fixture')
            if url.endswith('services.json'):
                return 200, json.dumps({'updated': stamp}).encode()
            return 200, json.dumps([entry]).encode()
        with mock.patch.object(helper, 'request', side_effect=response), mock.patch.object(helper.time, 'sleep'):
            for stamp in ['2099-01-01T00:00:00.76051149Z', '2099-01-01T00:00:00.7Z']:
                with self.subTest(stamp=stamp):
                    out = helper.result(record)
                    helper.home_confirm('https://home.example.ts.net', record, out)
                    self.assertTrue(out['listed'])
            entry['version'] = 'B'
            out = helper.result(record)
            helper.home_confirm('https://home.example.ts.net', record, out)
            self.assertFalse(out['listed'])

    def test_null_serve_status_is_an_empty_slot(self):
        self.save_config(None)
        self.assertEqual(helper.serve_status(), {})
        code, out = self.publish()
        self.assertEqual(code, 0)
        self.assertTrue(out['published'])

    def test_443_requires_a_whole_free_slot(self):
        occupied = {'TCP': {'443': {'HTTPS': True}},
                    'Web': {'dev.example.ts.net:443': {'Handlers': {'/existing': {'Text': 'human'}}}}}
        self.save_config(occupied)
        code, _ = self.publish('blocked', '--ports', '443')
        self.assertEqual(code, 1)
        self.assertEqual(self.config(), occupied)
        self.assertFalse(any('--bg' in cmd for cmd in self.commands()))
        self.save_config({})
        code, out = self.publish('free', '--ports', '443')
        self.assertEqual(code, 0)
        self.assertEqual(out['url'], 'https://dev.example.ts.net/')

    def test_status_partial_record_retains_result_contract(self):
        self.assertEqual(self.publish()[0], 0)
        store = helper.Store()
        record = store.load('a')
        record['phase'] = 'root'
        store.save(record)
        config = self.config()
        del config['Web']['dev.example.ts.net:8443']['Handlers'][helper.CARD_PATH]
        self.save_config(config)
        before = store.path('a').read_bytes()
        code, out = self.call('status', '--id', 'a')
        self.assertEqual(code, 1)
        self.assertEqual(set(out), set(helper.result(record)) | {'error'})
        self.assertEqual(out['id'], 'a')
        self.assertEqual(out['url'], record['url'])
        self.assertFalse(out['published'])
        self.assertFalse(out['app_verified'])
        self.assertFalse(out['card_verified'])
        self.assertIsNone(out['listed'])
        self.assertIn('Expected root/card mounts missing', out['warnings'][0])
        self.assertEqual(self.config(), config)
        self.assertEqual(store.path('a').read_bytes(), before)

    def test_status_app_failure_preserves_publication_fact(self):
        self.assertEqual(self.publish()[0], 0)
        before = self.config()
        with mock.patch.object(helper, 'request', side_effect=helper.Failure('HTTP 502')):
            code, out = self.call('status', '--id', 'a')
        self.assertEqual(code, 1)
        self.assertTrue(out['published'])
        self.assertFalse(out['app_verified'])
        self.assertFalse(out['card_verified'])
        self.assertIn('stage=loopback app', out['warnings'][0])
        self.assertIn('error=HTTP 502', out['error'])
        self.assertEqual(self.config(), before)

    def test_status_app_failure_still_confirms_exact_home_listing(self):
        self.assertEqual(self.publish()[0], 0)
        record = helper.Store().load('a')
        entry = self.catalog_entry(record)

        def response(url, **kwargs):
            if url.endswith('/refresh'):
                return 200, b''
            if url.endswith('/api/services.json'):
                return 200, b'{"updated":"2099-01-01T00:00:00.76051149Z"}'
            if url.endswith('/api/list.json'):
                return 200, json.dumps([entry]).encode()
            if url == record['url']:
                return 200, b'generic page without the identifying marker'
            return self.respond(url, **kwargs)

        with mock.patch.object(helper, 'request', side_effect=response), \
                mock.patch.object(helper.time, 'sleep'):
            code, out = self.call('status', '--id', 'a', '--home', 'https://home.example.ts.net')
        self.assertEqual(code, 1)
        self.assertTrue(out['published'])
        self.assertTrue(out['card_verified'])
        self.assertFalse(out['app_verified'])
        self.assertIs(out['listed'], True)

    def test_failed_status_with_home_reports_unconfirmed_listing(self):
        self.assertEqual(self.publish()[0], 0)
        config = self.config()
        del config['Web']['dev.example.ts.net:8443']['Handlers'][helper.CARD_PATH]
        self.save_config(config)
        with mock.patch.object(helper, 'request') as request:
            code, out = self.call('status', '--id', 'a', '--home', 'https://home.example.ts.net')
        self.assertEqual(code, 1)
        self.assertFalse(out['published'])
        self.assertIs(out['listed'], False)
        request.assert_not_called()

    def test_status_identity_failure_with_home_reports_unconfirmed_listing(self):
        self.assertEqual(self.publish()[0], 0)
        with mock.patch.object(helper, 'identity', side_effect=helper.Failure('fixture identity failure')), \
                mock.patch.object(helper, 'request') as request:
            code, out = self.call('status', '--id', 'a', '--home', 'https://home.example.ts.net')
        self.assertEqual(code, 1)
        self.assertFalse(out['published'])
        self.assertIs(out['listed'], False)
        request.assert_not_called()

    def test_cleanup_intent_dry_run_reports_record_discard(self):
        self.assertEqual(self.publish()[0], 0)
        store = helper.Store()
        record = store.load('a')
        record['phase'] = 'intent'
        store.save(record)
        before = store.path('a').read_bytes()
        config = self.config()
        code, out = self.call('cleanup', '--id', 'a', '--dry-run')
        self.assertEqual(code, 0)
        self.assertEqual(out['commands'], ['discard ownership record; no mounts were written'])
        self.assertEqual(store.path('a').read_bytes(), before)
        self.assertEqual(self.config(), config)

    def test_cleanup_dry_run_outputs_exact_removals_without_mutation(self):
        self.assertEqual(self.publish()[0], 0)
        config = self.config()
        path = helper.Store().path('a')
        before = path.read_bytes()
        code, out = self.call('cleanup', '--id', 'a', '--dry-run')
        self.assertEqual(code, 0)
        self.assertTrue(out['dry_run'])
        self.assertEqual(out['removed'], {})
        self.assertEqual(out['warnings'], [])
        self.assertEqual(out['commands'], [
            self.off(8443, helper.CARD_PATH), self.off(8443, '/')])
        self.assertEqual(self.config(), config)
        self.assertEqual(path.read_bytes(), before)
        self.assertFalse(any(cmd[-1] == 'off' for cmd in self.commands()))

    def test_list_set_flattens_metadata_and_preserves_plain_list(self):
        self.assertEqual(self.publish()[0], 0)
        self.card.write_text(json.dumps({'set': 'Other', 'title': 'Unrelated'}))
        self.assertEqual(self.publish('b')[0], 0)
        store = helper.Store()
        before = {name: store.path(name).read_bytes() for name in ('a', 'b')}
        with mock.patch.object(helper, 'identity') as identity, \
                mock.patch.object(helper, 'request') as request:
            code, out = self.call('list', '--set', '  Landing\u2003')
            self.assertEqual(code, 0)
            record = store.load('a')
            expected = dict(record, title="A · Owner's hero", description='Inspect café',
                            set='Landing', version='A')
            self.assertEqual(out, {'previews': [expected]})
            other = dict(store.load('b'), title='Unrelated', description=None,
                         set='Other', version=None)
            self.assertEqual(self.call('list')[1], {'previews': [expected, other]})
            identity.assert_not_called()
            request.assert_not_called()
        for name, raw in before.items():
            self.assertEqual(store.path(name).read_bytes(), raw)

    def test_list_set_exact_case_sensitive_and_optional_metadata(self):
        self.card.write_text(json.dumps({'set': ' Landing '}))
        self.assertEqual(self.publish('a')[0], 0)
        self.card.write_text(json.dumps({'set': 'landing'}))
        self.assertEqual(self.publish('b')[0], 0)
        self.card.write_text('{}')
        self.assertEqual(self.publish('single')[0], 0)
        code, out = self.call('list', '--set', 'Landing')
        self.assertEqual(code, 0)
        self.assertEqual([item['id'] for item in out['previews']], ['a'])
        self.assertIsNone(out['previews'][0]['title'])
        self.assertIsNone(out['previews'][0]['version'])
        self.assertEqual([item['id'] for item in self.call('list', '--set', 'landing')[1]['previews']], ['b'])
        self.assertEqual(self.call('list', '--set', 'Land')[1], {'previews': []})

    def test_plain_list_retains_malformed_cards_and_all_record_fields(self):
        self.assertEqual(self.publish()[0], 0)
        store = helper.Store()
        record = store.load('a')
        record.update(card='{"set":', recovery_note={'keep': True})
        store.save(record)
        before = store.path('a').read_bytes()
        with mock.patch.object(helper, 'cli_argv', side_effect=AssertionError('CLI resolution')), \
                mock.patch.object(helper, 'request', side_effect=AssertionError('remote catalog')):
            code, out = self.call('list')
        self.assertEqual(code, 0)
        item = out['previews'][0]
        for key, value in record.items():
            self.assertEqual(item[key], value)
        for key in ('title', 'description', 'set', 'version'):
            self.assertIsNone(item[key])
        self.assertIn('Malformed recorded card', item['warnings'][0])
        self.assertEqual(store.path('a').read_bytes(), before)

    def test_failed_verification_preserves_original_diagnostics_when_compensation_fails(self):
        def response(url, **kwargs):
            if url.startswith('https:'):
                raise helper.Failure('HTTP 502 upstream detail')
            return self.respond(url, **kwargs)
        with mock.patch.object(helper, 'request', side_effect=response), \
                mock.patch.object(helper, 'remove_owned', side_effect=helper.Failure('changed owned root')):
            code, out = self.publish()
        self.assertEqual(code, 1)
        warning = out['warnings'][0]
        for detail in ('stage=HTTPS card', 'route=https://dev.example.ts.net:8443' + helper.CARD_PATH,
                       'transport=tailscale nc / certificate-checked TLS', 'elapsed_ms=',
                       'error=HTTP 502 upstream detail'):
            self.assertIn(detail, warning)
        self.assertFalse(out['compensation_completed'])
        self.assertTrue(out['ownership_record_remains'])
        self.assertIn('changed owned root', out['warnings'][1])

    def test_initial_loopback_failure_reports_probe_without_writing_mounts(self):
        with mock.patch.object(helper, 'request', side_effect=helper.Failure('connection refused')):
            code, out = self.publish()
        self.assertEqual(code, 1)
        for detail in ('stage=loopback app', 'route=http://127.0.0.1:3000/',
                       'transport=direct HTTP', 'elapsed_ms=', 'error=connection refused'):
            self.assertIn(detail, out['warnings'][0])
        self.assertTrue(out['compensation_completed'])
        self.assertFalse(out['ownership_record_remains'])
        self.assertFalse(helper.Store().path('a').exists())
        self.assertFalse(any('--bg' in command for command in self.commands()))

    def test_set_selectors_validate_before_cli_or_mutation(self):
        for command in ('list', 'cleanup'):
            for name in ('', ' \u2003 ', 'x' * 61, 'Land\ning', '\nLanding',
                         'Land\u2028ing', 'Land\u2029ing', 'Land\x00ing'):
                with self.subTest(command=command, name=name), \
                        mock.patch.object(helper, 'run') as run:
                    code, out = self.call(command, '--set', name)
                    self.assertEqual(code, 1)
                    self.assertIn('error', out)
                    run.assert_not_called()
        self.assertEqual(helper.set_value('  ' + 'é' * 60 + '\u2003'), 'é' * 60)

    def test_set_cleanup_preserves_other_sets_and_unowned_mounts(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        self.card.write_text(json.dumps({'set': 'landing'}))
        self.assertEqual(self.publish('other')[0], 0)
        config = self.config()
        config['Web']['dev.example.ts.net:8443']['Handlers']['/foreign'] = {'Text': 'human'}
        config['TCP']['9443'] = {'HTTPS': True}
        config['Web']['dev.example.ts.net:9443'] = {'Handlers': {
            '/': {'Proxy': 'http://127.0.0.1:9000'},
            helper.CARD_PATH: {'Text': json.dumps({'set': 'Landing'})}}}
        self.save_config(config)
        store = helper.Store()
        other = store.path('other').read_bytes()
        code, out = self.call('cleanup', '--set', ' Landing ')
        self.assertEqual(code, 0)
        self.assertEqual(out['set'], 'Landing')
        self.assertEqual(out['selected'], 2)
        self.assertFalse(out['noop'])
        self.assertEqual([item['id'] for item in out['results']], ['a', 'b'])
        self.assertTrue(all(not item['warnings'] for item in out['results']))
        self.assertEqual(self.config()['Web']['dev.example.ts.net:8443']['Handlers'],
                         {'/foreign': {'Text': 'human'}})
        self.assertNotIn('dev.example.ts.net:8444', self.config()['Web'])
        for hp in ('dev.example.ts.net:8445', 'dev.example.ts.net:9443'):
            self.assertEqual(self.config()['Web'][hp], config['Web'][hp])
        self.assertEqual(store.path('other').read_bytes(), other)
        self.assertFalse(store.path('a').exists())
        self.assertFalse(store.path('b').exists())

    def test_set_cleanup_conflict_continues_independent_members(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        config = self.config()
        config['Web']['dev.example.ts.net:8443']['Handlers']['/'] = {'Proxy': 'http://127.0.0.1:9999'}
        self.save_config(config)
        code, out = self.call('cleanup', '--set', 'Landing')
        self.assertEqual(code, 1)
        self.assertEqual(out['selected'], 2)
        a, b = out['results']
        self.assertEqual(a['id'], 'a')
        self.assertTrue(a['warnings'])
        self.assertTrue(a['removed'][helper.CARD_PATH])
        self.assertFalse(a['removed'].get('/'))
        self.assertEqual(b['id'], 'b')
        self.assertEqual(b['warnings'], [])
        self.assertEqual(b['removed'], {helper.CARD_PATH: True, '/': True})
        self.assertEqual(self.config()['Web']['dev.example.ts.net:8443']['Handlers'],
                         {'/': {'Proxy': 'http://127.0.0.1:9999'}})
        self.assertNotIn('dev.example.ts.net:8444', self.config()['Web'])
        self.assertTrue(helper.Store().path('a').exists())
        self.assertFalse(helper.Store().path('b').exists())

    def test_set_selection_skips_malformed_records_with_warnings(self):
        self.assertEqual(self.publish()[0], 0)
        store = helper.Store()
        malformed = {
            'missing-card': json.dumps({'id': 'missing-card'}),
            'truncated-record': '{"card":',
            'truncated-card': json.dumps({'card': '{"set":'}),
            'wrong-card-type': json.dumps({'card': None}),
        }
        for name, raw in malformed.items():
            store.path(name).write_text(raw)
        warnings = io.StringIO()
        with contextlib.redirect_stderr(warnings):
            records = helper.set_records(store, 'Landing')
        self.assertEqual([record['id'] for record in records], ['a'])
        for name in malformed:
            self.assertIn('Skipping malformed ownership record ' + name + '.json:', warnings.getvalue())
        code, out = self.call('list', '--set', 'Landing')
        self.assertEqual(code, 0)
        self.assertEqual([record['id'] for record in out['previews']], ['a'])
        code, out = self.call('cleanup', '--set', 'Landing')
        self.assertEqual(code, 0)
        self.assertEqual(out['selected'], 1)
        self.assertEqual(out['results'][0]['warnings'], [])
        self.assertFalse(store.path('a').exists())
        for name, raw in malformed.items():
            self.assertEqual(store.path(name).read_text(), raw)

    def test_set_cleanup_structurally_broken_member_continues(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        store = helper.Store()
        record = store.load('a')
        del record['upstream']
        del record['removed']
        store.save(record)
        before = self.config()['Web']['dev.example.ts.net:8443']
        code, out = self.call('cleanup', '--set', 'Landing')
        self.assertEqual(code, 1)
        self.assertEqual(out['selected'], 2)
        self.assertEqual(out['results'][0]['removed'], {})
        self.assertIn('upstream', out['results'][0]['warnings'][0])
        self.assertEqual(out['results'][1]['warnings'], [])
        self.assertEqual(self.config()['Web']['dev.example.ts.net:8443'], before)
        self.assertTrue(store.path('a').exists())
        self.assertFalse(store.path('b').exists())

    def test_empty_set_cleanup_is_explicit_noop(self):
        self.assertEqual(self.publish()[0], 0)
        before = self.config()
        raw = helper.Store().path('a').read_bytes()
        for extra in ((), ('--dry-run',)):
            with self.subTest(extra=extra), mock.patch.object(helper, 'run') as run:
                code, out = self.call('cleanup', '--set', 'landing', *extra)
                self.assertEqual(code, 0)
                self.assertEqual(out, {'set': 'landing', 'selected': 0, 'results': [],
                                       'dry_run': bool(extra), 'noop': True})
                run.assert_not_called()
        self.assertEqual(self.config(), before)
        self.assertEqual(helper.Store().path('a').read_bytes(), raw)

    def test_set_cleanup_dry_run_exact_commands_and_no_writes(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        before = self.config()
        store = helper.Store()
        records = {name: store.path(name).read_bytes() for name in ('a', 'b')}
        for _ in range(2):
            code, out = self.call('cleanup', '--set', 'Landing', '--dry-run')
            self.assertEqual(code, 0)
            self.assertTrue(out['dry_run'])
            self.assertEqual(out['selected'], 2)
            for item, name, port in zip(out['results'], ('a', 'b'), (8443, 8444)):
                self.assertEqual(item['id'], name)
                self.assertEqual(item['removed'], {})
                self.assertEqual(item['warnings'], [])
                self.assertEqual(item['commands'], [
                    self.off(port, helper.CARD_PATH), self.off(port, '/')])
        self.assertEqual(self.config(), before)
        for name, raw in records.items():
            self.assertEqual(store.path(name).read_bytes(), raw)
        self.assertFalse(any(cmd[-1] == 'off' for cmd in self.commands()))

    def test_set_cleanup_refuses_foreign_host_but_continues_local_member(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        store = helper.Store()
        record = store.load('a')
        before = self.config()['Web']['dev.example.ts.net:8443']
        for field, foreign in (('node', 'other-node'), ('fqdn', 'other.example.ts.net')):
            with self.subTest(field=field):
                altered = dict(record, **{field: foreign})
                store.save(altered)
                raw = store.path('a').read_bytes()
                code, out = self.call('cleanup', '--set', 'Landing', '--dry-run')
                self.assertEqual(code, 1)
                self.assertEqual(out['selected'], 2)
                self.assertIn('node/FQDN differs', out['results'][0]['warnings'][0])
                self.assertEqual(out['results'][0]['commands'], [])
                self.assertEqual(len(out['results'][1]['commands']), 2)
                self.assertEqual(store.path('a').read_bytes(), raw)
        code, out = self.call('cleanup', '--set', 'Landing')
        self.assertEqual(code, 1)
        self.assertTrue(out['results'][0]['warnings'])
        self.assertEqual(out['results'][1]['warnings'], [])
        self.assertEqual(self.config()['Web']['dev.example.ts.net:8443'], before)
        self.assertNotIn('dev.example.ts.net:8444', self.config()['Web'])
        self.assertTrue(store.path('a').exists())
        self.assertFalse(store.path('b').exists())

    def test_cleanup_requires_exactly_one_selector(self):
        for args in (('cleanup',), ('cleanup', '--id', 'a', '--set', 'Landing')):
            with self.subTest(args=args), \
                    contextlib.redirect_stderr(io.StringIO()), \
                    self.assertRaises(SystemExit) as exc:
                helper.main(list(args))
            self.assertEqual(exc.exception.code, 2)

    def test_set_cleanup_dry_run_conflict_still_plans_other_members(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        config = self.config()
        config['Web']['dev.example.ts.net:8443']['Handlers'][helper.CARD_PATH] = {'Text': 'human'}
        self.save_config(config)
        store = helper.Store()
        records = {name: store.path(name).read_bytes() for name in ('a', 'b')}
        code, out = self.call('cleanup', '--set', 'Landing', '--dry-run')
        self.assertEqual(code, 1)
        self.assertEqual(out['selected'], 2)
        self.assertTrue(out['results'][0]['warnings'])
        self.assertEqual(out['results'][0]['commands'], [
            self.off(8443, '/')])
        self.assertEqual(out['results'][1]['warnings'], [])
        self.assertEqual(len(out['results'][1]['commands']), 2)
        self.assertEqual(self.config(), config)
        for name, raw in records.items():
            self.assertEqual(store.path(name).read_bytes(), raw)
        self.assertFalse(any(cmd[-1] == 'off' for cmd in self.commands()))

    def test_set_cleanup_snapshots_members_while_holding_lock(self):
        self.assertEqual(self.publish('a')[0], 0)
        self.assertEqual(self.publish('b')[0], 0)
        store = helper.Store()
        remove = helper.remove_owned
        selected = []
        def remove_with_late_member(locked_store, record, dry_run=False):
            fd = os.open(locked_store.directory / 'lock', os.O_RDWR)
            try:
                with self.assertRaises(BlockingIOError):
                    helper.fcntl.flock(fd, helper.fcntl.LOCK_EX | helper.fcntl.LOCK_NB)
            finally:
                os.close(fd)
            selected.append(record['id'])
            if record['id'] == 'a':
                late = dict(store.load('b'), id='late')
                store.save(late)
            return remove(locked_store, record, dry_run)
        with mock.patch.object(helper, 'remove_owned', side_effect=remove_with_late_member):
            code, out = self.call('cleanup', '--set', 'Landing', '--dry-run')
        self.assertEqual(code, 0)
        self.assertEqual(out['selected'], 2)
        self.assertEqual(selected, ['a', 'b'])
        self.assertTrue(store.path('late').exists())

    def test_home_confirmation_releases_lock_for_publish_and_status(self):
        def confirm(home, record, out):
            fd = os.open(helper.Store().directory / 'lock', os.O_RDWR)
            try:
                helper.fcntl.flock(fd, helper.fcntl.LOCK_EX | helper.fcntl.LOCK_NB)
            finally:
                os.close(fd)
        with mock.patch.object(helper, 'home_confirm', side_effect=confirm) as confirmation:
            self.assertEqual(self.publish()[0], 0)
            self.assertEqual(self.call('status', '--id', 'a')[0], 0)
        self.assertEqual(confirmation.call_count, 2)

    def test_status_freshness_uses_current_attempt_not_publication(self):
        self.assertEqual(self.publish()[0], 0)
        store = helper.Store()
        record = store.load('a')
        record['attempted_at'] = helper.catalog_time('1999-01-01T00:00:00Z')
        store.save(record)
        entry = self.catalog_entry(record)
        def response(url, **kwargs):
            if url.endswith('/refresh'):
                return 200, b''
            if url.endswith('/api/services.json'):
                return 200, b'{"updated":"2000-01-01T00:00:00.76051149Z"}'
            if url.endswith('/api/list.json'):
                return 200, json.dumps([entry]).encode()
            return self.respond(url, **kwargs)
        with mock.patch.object(helper, 'request', side_effect=response), \
                mock.patch.object(helper.time, 'time', return_value=helper.catalog_time('2001-01-01T00:00:00Z')), \
                mock.patch.object(helper.time, 'sleep'):
            code, out = self.call('status', '--id', 'a', '--home', 'https://home.example.ts.net')
        self.assertEqual(code, 0)
        self.assertTrue(out['published'])
        self.assertFalse(out['listed'])
        self.assertEqual(store.load('a')['attempted_at'], record['attempted_at'])

    def test_titleless_card_accepts_home_fallback_label(self):
        self.card.write_text('{}')
        self.assertEqual(self.publish()[0], 0)
        record = helper.Store().load('a')
        def response(url, **kwargs):
            if url.endswith('/refresh'):
                return 200, b''
            if url.endswith('services.json'):
                return 200, b'{"updated":"2099-01-01T00:00:00.76051149Z"}'
            return 200, json.dumps([{'url': record['url'], 'label': 'Dev :8443'}]).encode()
        out = helper.result(record)
        with mock.patch.object(helper, 'request', side_effect=response):
            helper.home_confirm('https://home.example.ts.net', record, out)
        self.assertTrue(out['listed'])



class TransportTests(unittest.TestCase):
    commands = HelperTests.commands
    call = HelperTests.call
    publish = HelperTests.publish
    respond = HelperTests.respond

    def tun_off(self):
        status = json.loads(helper.run(['status', '--json']))
        status['TUN'] = False
        (self.root / 'status').write_text(json.dumps(status))

    @classmethod
    def setUpClass(cls):
        cls.cert_directory = tempfile.TemporaryDirectory()
        root = Path(cls.cert_directory.name)
        cls.cert, cls.key = root / 'cert.pem', root / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-keyout', str(cls.key), '-out', str(cls.cert), '-days', '1',
                        '-subj', '/CN=dev.example.ts.net', '-addext',
                        'subjectAltName=DNS:dev.example.ts.net,DNS:home.example.ts.net'],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        cls.addClassCleanup(cls.cert_directory.cleanup)

    def setUp(self):
        HelperTests.setUp(self)
        self.network.stop()
        self.requests = []
        self.server_errors = []

    @contextlib.contextmanager
    def server(self, response):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(self.cert), str(self.key))
        listener = socket.socket()
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        listener.settimeout(3)
        def serve():
            responses = response if isinstance(response, list) else [response]
            for payload in responses:
                try:
                    raw, _ = listener.accept()
                    with raw:
                        raw.settimeout(3)
                        with context.wrap_socket(raw, server_side=True) as connection:
                            data = b''
                            while b'\r\n\r\n' not in data:
                                data += connection.recv(4096)
                            self.requests.append(data)
                            connection.sendall(payload)
                except ssl.SSLError:
                    pass  # The hostname-rejection test intentionally aborts TLS.
                except Exception as exc:
                    self.server_errors.append(exc)
        thread = threading.Thread(target=serve)
        thread.start()
        try:
            with mock.patch.dict(os.environ, {'SSL_CERT_FILE': str(self.cert),
                                             'NC_PORT': str(listener.getsockname()[1])}):
                yield
        finally:
            thread.join(4)
            listener.close()
        self.assertFalse(thread.is_alive())
        self.assertEqual(self.server_errors, [])

    def test_transport_content_length_limit_and_prefix(self):
        for prefix in (False, True):
            with self.subTest(prefix=prefix), self.server(
                    b'HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\n0123456789'):
                if prefix:
                    self.assertEqual(helper.request('https://dev.example.ts.net:8443/',
                                                    limit=4, prefix=True), (200, b'0123'))
                else:
                    with self.assertRaisesRegex(helper.Failure, 'Response exceeds size limit'):
                        helper.request('https://dev.example.ts.net:8443/', limit=4)
        self.assertIn(b'Host: dev.example.ts.net:8443\r\n', self.requests[0])
        self.assertIn(b'User-Agent: tailnet-preview\r\n', self.requests[0])
        self.assertIn(b'Connection: close\r\n', self.requests[0])
        self.assertIn(['nc', 'dev.example.ts.net', '8443'], self.commands())
        self.assertEqual(self.commands().count(['status', '--json']), 1)

    def test_transport_chunked_and_post_refresh(self):
        with self.server(b'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n'
                         b'3\r\nabc\r\n2\r\nde\r\n0\r\nX-Trailer: yes\r\n\r\n'):
            self.assertEqual(helper.request('https://home.example.ts.net/refresh',
                                            method='POST'), (200, b'abcde'))
        self.assertTrue(self.requests[0].startswith(b'POST /refresh HTTP/1.1\r\n'))
        self.assertIn(b'Content-Length: 0\r\n', self.requests[0])

    def test_transport_redirect_refused(self):
        with self.server(b'HTTP/1.1 303 See Other\r\nLocation: https://elsewhere.test/\r\n'
                         b'Content-Length: 0\r\n\r\n'):
            with self.assertRaisesRegex(helper.Failure,
                    'HTTP 303 at .*; redirect not followed: https://elsewhere.test/'):
                helper.request('https://dev.example.ts.net/')
        self.assertEqual(sum(command[:1] == ['nc'] for command in self.commands()), 1)

    def test_transport_hostname_mismatch_rejected(self):
        (self.root / 'status').write_text(json.dumps({
            'BackendState': 'Running', 'Self': {'DNSName': 'wrong.example.ts.net.'}}))
        with self.server(b'HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n'):
            with self.assertRaisesRegex(helper.Failure, 'certificate verify failed.*[Hh]ostname mismatch'):
                helper.request('https://wrong.example.ts.net/')
        self.assertEqual(self.requests, [])

    def test_transport_deadline_kills_and_reaps_cli(self):
        helper.tailnet_status()
        processes = []
        original_popen = subprocess.Popen
        def start(*args, **kwargs):
            process = original_popen(*args, **kwargs)
            processes.append(process)
            return process
        started = time.monotonic()
        with mock.patch.dict(os.environ, {'NC_STALL': '1'}), \
                mock.patch.object(helper.subprocess, 'Popen', side_effect=start):
            with self.assertRaisesRegex(helper.Failure, 'deadline exceeded'):
                helper.request('https://dev.example.ts.net/', timeout=0.3)
        self.assertLess(time.monotonic() - started, 2)
        self.assertEqual(len(processes), 1)
        self.assertIsNotNone(processes[0].returncode)
        pid = processes[0].pid
        with self.assertRaises(ProcessLookupError):
            os.kill(pid, 0)
        with self.assertRaises(ChildProcessError):
            os.waitpid(pid, os.WNOHANG)

    def test_transport_service_record_and_selected_cli(self):
        (self.root / 'status').write_text(json.dumps({
            'BackendState': 'Running', 'TUN': False,
            'Self': {'DNSName': 'dev.example.ts.net.'},
            'ExtraRecords': [{'Name': 'home.example.ts.net.', 'Value': '100.64.0.2'}]}))
        selected = self.root / 'Selected Tailscale CLI'
        selected.write_text(FAKE)
        selected.chmod(0o700)
        with self.server(b'HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok'), \
                mock.patch.dict(os.environ, {'TAILSCALE': str(selected)}), \
                mock.patch.object(helper.subprocess, 'Popen', wraps=subprocess.Popen) as popen:
            self.assertEqual(helper.request('https://home.example.ts.net/'), (200, b'ok'))
        self.assertEqual(popen.call_args.args[0], [str(selected), 'nc', 'home.example.ts.net', '443'])

    def test_userspace_publication_verifies_card_and_app_with_checked_tls(self):
        self.tun_off()
        card = dict(json.loads(self.card.read_text()), preview=True)
        body = json.dumps(card, ensure_ascii=False).encode()
        response = lambda payload: (b'HTTP/1.1 200 OK\r\nContent-Length: '
                                    + str(len(payload)).encode() + b'\r\n\r\n' + payload)
        loopback = mock.MagicMock(status=200)
        loopback.__enter__.return_value = loopback
        loopback.read.return_value = b'expected screen'
        with self.server([response(body), response(b'expected screen')]), \
                mock.patch.object(helper.OPENER, 'open', return_value=loopback) as opener:
            code, out = self.publish()
        self.assertEqual(code, 0)
        self.assertTrue(out['published'] and out['app_verified'] and out['card_verified'])
        self.assertEqual(len(self.requests), 2)
        self.assertIn(('GET ' + helper.CARD_PATH + ' HTTP/1.1').encode(), self.requests[0])
        self.assertTrue(self.requests[1].startswith(b'GET / HTTP/1.1'))
        self.assertTrue(all(call.args[0].full_url.startswith('http://127.0.0.1:')
                            for call in opener.call_args_list))

    def test_userspace_app_transport_failure_reports_stage_and_compensation(self):
        self.tun_off()
        loopback = mock.MagicMock(status=200)
        loopback.__enter__.return_value = loopback
        loopback.read.return_value = b'expected screen'
        real_request = helper.request
        def request(url, **kwargs):
            if url.endswith(helper.CARD_PATH):
                return self.respond(url, **kwargs)
            return real_request(url, **kwargs)
        with mock.patch.object(helper.OPENER, 'open', return_value=loopback), \
                mock.patch.object(helper, 'request', side_effect=request), \
                mock.patch.dict(os.environ, {'NC_STALL': '1'}), \
                mock.patch.object(helper, 'expected_app', wraps=helper.expected_app):
            # Exercise the real userspace stream deadline without a long fixture wait.
            def bounded_request(url, **kwargs):
                kwargs['timeout'] = 0.2
                return request(url, **kwargs)
            with mock.patch.object(helper, 'request', side_effect=bounded_request):
                code, out = self.publish()
        self.assertEqual(code, 1)
        for detail in ('stage=HTTPS app', 'route=https://dev.example.ts.net:8443/',
                       'transport=tailscale nc / certificate-checked TLS', 'elapsed_ms=',
                       'deadline exceeded'):
            self.assertIn(detail, out['warnings'][0])
        self.assertTrue(out['card_verified'])
        self.assertFalse(out['app_verified'])
        self.assertTrue(out['compensation_completed'])
        self.assertFalse(out['ownership_record_remains'])


class ValidationTests(unittest.TestCase):
    def test_card_boundaries(self):
        for field, limit in [('title', 60), ('description', 140), ('set', 60), ('icon', 64)]:
            with self.subTest(field=field):
                self.assertEqual(helper.card_value(json.dumps({field: 'a' * limit}).encode())[field], 'a' * limit)
                with self.assertRaises(helper.Failure):
                    helper.card_value(json.dumps({field: 'a' * (limit + 1)}).encode())
        invalid = [b'null', b'[]', b'{} {}', b'{"title":null}', b'{"preview":1}',
                   b'{"x":1,"x":2}', b'{"title":"a\\nb"}', b'{"icon":"https://x"}',
                   b'{"version":"ABC"}', b'{"title":"a\\u2028b"}', b' ' * 4097,
                   b'{"title":"\\u001c trim"}', b'{"description":"trim\\u001f"}',
                   b'{"title":"\\ntrim"}']
        for raw in invalid:
            with self.subTest(raw=raw[:80]), self.assertRaises(helper.Failure):
                helper.card_value(raw)
        self.assertEqual(helper.card_value(b'{"title":"  ","unknown":"ignored"}'), {})
        self.assertEqual(helper.card_value('{"title":"  café  "}'.encode()), {'title': 'café'})

    def test_port_pool(self):
        self.assertEqual(helper.ports_value('8444, 443, 8443-8444'), [443, 8443, 8444])
        for value in ['', '1,,2', '-1', '0', '65536', '4-1', '1-33', '1-32,40', 'a', '1-2-3']:
            with self.subTest(value=value), self.assertRaises(helper.Failure):
                helper.ports_value(value)

    def test_loopback_only(self):
        for url in ['http://localhost:3000', 'https://127.0.0.1:3000', 'http://127.0.0.1',
                    'http://user@127.0.0.1:3000', 'http://127.0.0.1:3000/?x=1', 'http://example.com:3000']:
            with self.subTest(url=url), self.assertRaises(helper.Failure):
                helper.upstream_value(url)
        self.assertEqual(helper.upstream_value('http://[::1]:3000'), 'http://[::1]:3000')
        self.assertEqual(helper.upstream_value('HTTP://127.0.0.1:03000/'), 'http://127.0.0.1:3000')
        self.assertEqual(helper.upstream_value('HTTP://[::1]:03000/app/'), 'http://[::1]:3000/app')

    def test_cli_has_closed_stdin_and_replaces_invalid_bytes(self):
        done = mock.Mock(returncode=0, stdout='ok', stderr='')
        with mock.patch.object(helper.shutil, 'which', return_value='/fixture/tailscale'), \
                mock.patch.object(helper.subprocess, 'run', return_value=done) as run:
            self.assertEqual(helper.run(['serve', 'status', '--json']), 'ok')
        self.assertEqual(run.call_args.kwargs['stdin'], helper.subprocess.DEVNULL)
        self.assertEqual(run.call_args.kwargs['errors'], 'replace')

    def test_executable_selection(self):
        app = '/Applications/Tailscale.app/Contents/MacOS/Tailscale'
        cases = [
            ('linux', '/opt/CLI with spaces', {'/opt/CLI with spaces': '/opt/CLI with spaces'}, '/opt/CLI with spaces', ['/opt/CLI with spaces']),
            ('darwin', 'custom-cli', {'custom-cli': '/opt/custom-cli'}, '/opt/custom-cli', ['custom-cli']),
            ('darwin', '', {'tailscale': '/usr/local/bin/tailscale'}, '/usr/local/bin/tailscale', ['tailscale']),
            ('darwin', '', {app: app}, app, ['tailscale', app]),
            ('linux', '', {}, None, ['tailscale']),
            ('darwin', 'missing', {'tailscale': '/bin/tailscale', app: app}, None, ['missing']),
            ('darwin', 'tailscale --flag', {}, None, ['tailscale --flag']),
        ]
        for platform, override, found, expected, calls in cases:
            with self.subTest(platform=platform, override=override), \
                    mock.patch.object(helper.sys, 'platform', platform), \
                    mock.patch.dict(os.environ, {'TAILSCALE': override}), \
                    mock.patch.object(helper.shutil, 'which', side_effect=found.get) as which:
                if expected:
                    self.assertEqual(helper.cli_argv(['status', '--json']), [expected, 'status', '--json'])
                else:
                    with self.assertRaisesRegex(helper.Failure, 'Cannot find the Tailscale CLI.*shell alias'):
                        helper.cli_argv([])
                self.assertEqual(which.call_args_list, [mock.call(value) for value in calls])

    def test_macos_environment_is_copied_and_errors_preserved(self):
        done = mock.Mock(returncode=0, stdout='ok', stderr='')
        with mock.patch.object(helper.sys, 'platform', 'darwin'), \
                mock.patch.dict(os.environ, {'TAILSCALE_BE_CLI': 'parent'}), \
                mock.patch.object(helper.shutil, 'which', return_value='/fixture/Tailscale app'), \
                mock.patch.object(helper.subprocess, 'run', return_value=done) as run:
            helper.run(['serve', 'status', '--json'])
            self.assertEqual(run.call_args.args[0], ['/fixture/Tailscale app', 'serve', 'status', '--json'])
            self.assertEqual(run.call_args.kwargs['env']['TAILSCALE_BE_CLI'], '1')
            self.assertEqual(os.environ['TAILSCALE_BE_CLI'], 'parent')
            self.assertEqual(run.call_args.kwargs['timeout'], helper.TIMEOUT)
            done.returncode, done.stderr = 1, 'administrator consent required'
            with self.assertRaisesRegex(helper.Failure, 'administrator consent required'):
                helper.run(['serve', 'status'])
            for error in [OSError('fixture OS detail'), helper.subprocess.TimeoutExpired('fixture CLI', 8)]:
                run.side_effect = error
                with self.assertRaisesRegex(helper.Failure, 'fixture'):
                    helper.run(['status'])

    def test_native_windows_exits_before_importing_fcntl(self):
        source = SCRIPT.read_text()
        imports = __import__('builtins').__import__
        def guarded_import(name, *args, **kwargs):
            if name == 'fcntl':
                self.fail('fcntl imported on native Windows')
            return imports(name, *args, **kwargs)
        with mock.patch.object(helper.sys, 'platform', 'win32'), \
                mock.patch('builtins.__import__', side_effect=guarded_import), \
                self.assertRaisesRegex(SystemExit, 'Native Windows publication is not supported'):
            exec(compile(source, str(SCRIPT), 'exec'), {'__name__': 'windows_fixture'})

    def test_large_app_uses_bounded_prefix_but_card_keeps_limit(self):
        response = mock.MagicMock(status=200)
        response.__enter__.return_value = response
        body = b'expected screen' + b'x' * helper.MAX_BODY
        response.read.side_effect = lambda count: body[:count]
        record = {'expected_status': 200, 'expect_text': ''}
        with mock.patch.object(helper, 'request', wraps=helper.request), \
                mock.patch.object(helper.OPENER, 'open', return_value=response):
            helper.expected_app('http://127.0.0.1:3000/', record)
            response.read.assert_called_with(helper.MAX_BODY)
            record['expect_text'] = 'expected screen'
            helper.expected_app('http://127.0.0.1:3000/', record)
            with self.assertRaises(helper.Failure):
                helper.request('http://127.0.0.1:3000' + helper.CARD_PATH, limit=4096)
            response.read.assert_called_with(4097)


if __name__ == '__main__':
    unittest.main()
