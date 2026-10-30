#!/usr/bin/env python3
# /// script
# requires-python = ">=3.9"
# dependencies = []
# ///
"""Publish private, owned Tailscale Serve previews. Python 3.9+, Unix only."""
import argparse
import contextlib
import datetime
import sys

if sys.platform == 'win32':
    sys.exit('tailnet-preview requires Unix (Linux or macOS) and Python 3.9+. Native Windows publication is not supported. Windows can still run tailhome and open previews.')

import fcntl
import functools
import getpass
import http.client
import io
import json
import os
from pathlib import Path
import re
import subprocess
import shutil
import select
import socket
import ssl
import tempfile
import time
import unicodedata
import urllib.error
import urllib.parse
import urllib.request

CARD_PATH = '/.well-known/tailhome.json'
TIMEOUT = 8
MAX_BODY = 1024 * 1024
APP_CHECK_TIMEOUT = 3  # per preview in `list`, which may check several
OPERATOR_FIX = 'The owner can run sudo tailscale set --operator=$USER once, then retry.'


class Failure(Exception):
    pass


def require(ok, message):
    if not ok:
        raise Failure(message)


def object_pairs(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'Duplicate JSON field: ' + key)
        result[key] = value
    return result


def decode(data):
    try:
        return json.loads(data, object_pairs_hook=object_pairs,
                          parse_constant=lambda s: (_ for _ in ()).throw(Failure('Invalid JSON number')))
    except (ValueError, UnicodeError) as exc:
        raise Failure('Invalid UTF-8 JSON: ' + str(exc)) from exc


def card_value(raw):
    """Validate the same plain-text card limits as tailhome before publishing."""
    require(len(raw) <= 4096, 'Card exceeds 4096 bytes')
    card = decode(raw.decode('utf-8') if isinstance(raw, bytes) else raw)
    require(isinstance(card, dict), 'Card must be one JSON object')
    result = {}
    for key, limit in [('title', 60), ('description', 140), ('icon', 64), ('set', 60), ('version', 2)]:
        if key not in card:
            continue
        value = card[key]
        require(isinstance(value, str), key + ' must be a string, not null')
        require(not any(unicodedata.category(c) == 'Cc' or c in '\u2028\u2029' for c in value),
                key + ' contains control characters or line separators')
        value = value.strip()
        require(len(value) <= limit, key + ' exceeds ' + str(limit) + ' code points')
        if value:
            if key == 'icon':
                require(re.fullmatch('[a-z0-9-]+', value), 'icon must be a dashboard-icons slug')
            if key == 'version':
                require(re.fullmatch('[A-Za-z0-9]{1,2}', value), 'version must be 1 or 2 ASCII letters or digits')
            result[key] = value
    if 'preview' in card:
        require(type(card['preview']) is bool, 'preview must be boolean')
        result['preview'] = card['preview']
    return result


def set_value(text):
    value = card_value(json.dumps({'set': text}).encode()).get('set')
    require(value, 'Set selector must not be empty')
    return value


def set_records(store, name):
    """Select only local ownership records by exact validated card metadata."""
    selected = []
    for path in sorted(store.directory.glob('*.json')):
        try:
            record = store.load(path.stem)
            card = card_value(record['card'])
        except (Failure, OSError, KeyError, TypeError, UnicodeError) as exc:
            print('Skipping malformed ownership record ' + path.name + ': ' + str(exc), file=sys.stderr)
            continue
        if card.get('set') == name:
            selected.append(record)
    return selected


def ports_value(text):
    require(bool(text.strip()), 'No approved HTTPS ports configured. Ask the user to approve a pool on this machine (for example 8443-8452), then pass --ports or set TAILNET_PREVIEW_PORTS. Do not use 443 without explicit approval.')
    ports = set()
    for token in text.split(','):
        token = token.strip()
        require(re.fullmatch(r'[0-9]+(?:-[0-9]+)?', token), 'Invalid approved port token: ' + token)
        ends = list(map(int, token.split('-')))
        low, high = ends[0], ends[-1]
        require(1 <= low <= high <= 65535 and high - low < 32, 'Invalid or oversized port range: ' + token)
        ports.update(range(low, high + 1))
        require(len(ports) <= 32, 'At most 32 approved ports are allowed')
    return sorted(ports)


def upstream_value(url):
    """Return the canonical loopback proxy target stored by Tailscale Serve."""
    try:
        p = urllib.parse.urlsplit(url)
        require(p.scheme == 'http' and p.hostname in ('127.0.0.1', '::1') and p.port is not None
                and not p.username and not p.password and not p.query and not p.fragment
                and not any(unicodedata.category(c) == 'Cc' for c in url),
                'Upstream must be literal loopback HTTP with explicit port and no credentials, query or fragment')
        require(1 <= p.port <= 65535, 'Invalid upstream port')
    except ValueError as exc:
        raise Failure('Invalid upstream URL') from exc
    host = '[::1]' if p.hostname == '::1' else p.hostname
    return 'http://' + host + ':' + str(p.port) + p.path.rstrip('/')


def cli_argv(args):
    """Resolve an operator-selected executable without shell interpretation."""
    override = os.environ.get('TAILSCALE', '')
    executable = shutil.which(override) if override else shutil.which('tailscale')
    if not override and not executable and sys.platform == 'darwin':
        executable = shutil.which('/Applications/Tailscale.app/Contents/MacOS/Tailscale')
    require(executable, "Cannot find the Tailscale CLI. Set TAILSCALE to its executable path in the agent's launch environment. On macOS, the app normally includes /Applications/Tailscale.app/Contents/MacOS/Tailscale. A shell alias is not enough.")
    return [executable] + args


def cli_env():
    environment = os.environ.copy()
    if sys.platform == 'darwin':
        environment['TAILSCALE_BE_CLI'] = '1'
    return environment


def run(args):
    """Run only the authorized CLI, without interactive input or shell parsing."""
    argv = cli_argv(args)
    try:
        done = subprocess.run(argv, capture_output=True, text=True, env=cli_env(),
                              stdin=subprocess.DEVNULL, errors='replace', timeout=TIMEOUT)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise Failure('Cannot run authorized tailscale CLI: ' + str(exc)) from exc
    error = 'tailscale ' + ' '.join(args[:3]) + ' failed: ' + done.stderr.strip()
    # Recent CLIs already print the operator hint; add it only when they don't.
    if args and args[0] == 'serve' and args[1:2] != ['status'] and re.search(
            r'access denied|permission denied|not permitted', done.stderr, re.IGNORECASE) \
            and '--operator' not in done.stderr:
        error += ' ' + OPERATOR_FIX
    require(done.returncode == 0, error)
    return done.stdout


@functools.lru_cache(maxsize=1)
def tailnet_status():
    """Share one status snapshot between identity and MagicDNS resolution."""
    data = decode(run(['status', '--json']))
    require(isinstance(data, dict) and data.get('BackendState') == 'Running', 'Tailscale must already be connected')
    return data


def identity():
    data = tailnet_status()
    node = data.get('Self') or {}
    fqdn, stable = node.get('DNSName', '').rstrip('.').lower(), node.get('ID')
    require(isinstance(stable, str) and stable and re.fullmatch(r'[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?', fqdn)
            and '.' in fqdn, 'Tailscale status lacks stable node ID or FQDN')
    return {'node': stable, 'fqdn': fqdn}


def mapping(value, where):
    require(isinstance(value, dict), 'Unrecognized Serve configuration at ' + where)
    return value


def port_key(key):
    require(isinstance(key, str) and re.fullmatch('[0-9]+', key) and 1 <= int(key) <= 65535,
            'Unrecognized Serve port')
    return int(key)


def host_port(key):
    require(isinstance(key, str) and ':' in key, 'Unrecognized Serve host:port')
    host, port = key.rsplit(':', 1)
    require(bool(host), 'Empty Serve hostname')
    return port_key(port)


def config_ports(config, service=False, foreground=False):
    """Validate the entire known schema before reasoning about free slots."""
    mapping(config, 'root')
    allowed = {'TCP', 'Web', 'Tun'} if service else {'TCP', 'Web', 'Services', 'AllowFunnel', 'Foreground'}
    require(not set(config) - allowed, 'Unrecognized Serve configuration fields; update/review helper before publishing')
    for key in allowed - {'Tun'}:
        if key in config:
            mapping(config[key], key)
    occupied = set()
    for key, handler in mapping(config.get('TCP') or {}, 'TCP').items():
        occupied.add(port_key(key))
        mapping(handler, 'TCP handler')
        require(not set(handler) - {'HTTPS', 'HTTP', 'TCPForward', 'TerminateTLS', 'ProxyProtocol'}, 'Unrecognized TCP handler')
        for field, value in handler.items():
            expected = bool if field in ('HTTPS', 'HTTP') else int if field == 'ProxyProtocol' else str
            require(type(value) is expected, 'Malformed TCP handler')
    for key, server in mapping(config.get('Web') or {}, 'Web').items():
        occupied.add(host_port(key))
        mapping(server, 'Web server')
        require(set(server) == {'Handlers'}, 'Unrecognized Web server fields')
        for path, handler in mapping(server['Handlers'], 'Handlers').items():
            require(isinstance(path, str) and path.startswith('/'), 'Malformed mount path')
            mapping(handler, 'HTTP handler')
            require(not set(handler) - {'Path', 'Proxy', 'Text', 'AcceptAppCaps', 'Redirect'}, 'Unrecognized HTTP handler')
            require(all(isinstance(v, str) if k != 'AcceptAppCaps' else isinstance(v, list)
                        and all(isinstance(x, str) for x in v) for k, v in handler.items()), 'Malformed HTTP handler')
    if service:
        require(type(config.get('Tun', False)) is bool, 'Malformed Service Tun mode')
        # Tun mode cannot safely be reduced to individual listening ports.
        require(not config.get('Tun'), 'Service Tun mode prevents safe port allocation')
        return occupied
    for key, value in mapping(config.get('AllowFunnel') or {}, 'AllowFunnel').items():
        occupied.add(host_port(key))
        require(type(value) is bool, 'Malformed Funnel flag')
    for name, value in mapping(config.get('Services') or {}, 'Services').items():
        require(isinstance(name, str) and name.startswith('svc:'), 'Malformed Service name')
        occupied.update(config_ports(value, service=True))
    for value in mapping(config.get('Foreground') or {}, 'Foreground').values():
        require(not foreground, 'Nested foreground configuration is unrecognized')
        occupied.update(config_ports(value, foreground=True))
    return occupied


def serve_status():
    config = decode(run(['serve', 'status', '--json']))
    # A nil ServeConfig marshals as null in the installed CLI.
    if config is None:
        config = {}
    config_ports(config)
    return config


def preflight(ports):
    """Inspect prerequisites without creating state or changing the daemon."""
    out = {'python_version': sys.version.split()[0], 'cli_path': None, 'cli_version': None,
           'backend_running': False, 'logged_in': False, 'dns_name': None,
           'machine_name': None, 'https_available': False, 'serve_write': 'unknown',
           'free_ports': None, 'machine_ports': None, 'blockers': [], 'warnings': []}
    try:
        out['cli_path'] = cli_argv([])[0]
    except Failure as exc:
        out['blockers'].append(str(exc))
        return out
    try:
        out['cli_version'] = run(['version']).strip().splitlines()[0]
    except Failure as exc:
        out['warnings'].append(str(exc))
    try:
        status = decode(run(['status', '--json']))
        require(isinstance(status, dict), 'Unrecognized Tailscale status')
        node = status.get('Self') or {}
        out['backend_running'] = status.get('BackendState') == 'Running'
        out['logged_in'] = bool(node.get('ID')) and status.get('BackendState') not in (
            'NeedsLogin', 'NoState')
        out['dns_name'] = node.get('DNSName') or None
        out['machine_name'] = node.get('HostName') or (
            out['dns_name'].split('.')[0] if out['dns_name'] else None)
        # Tailscale lists certificate domains only when HTTPS certificates are enabled;
        # MagicDNS alone does not mean Serve can get a certificate.
        out['https_available'] = bool(status.get('CertDomains'))
    except Failure as exc:
        out['warnings'].append(str(exc))
    if not out['backend_running'] or not out['logged_in']:
        out['blockers'].append('Tailscale must already be running and logged in.')
    elif not out['https_available']:
        out['blockers'].append('HTTPS certificates are not enabled. Ask a tailnet admin to turn on MagicDNS and HTTPS Certificates in the DNS settings of the admin console: https://console.tailscale.com/admin/dns')
    if sys.platform.startswith('linux'):
        if os.geteuid() == 0:
            out['serve_write'] = 'allowed'
        else:
            try:
                prefs = decode(run(['debug', 'prefs']))
                require(isinstance(prefs, dict), 'Unrecognized Tailscale preferences')
                out['serve_write'] = ('allowed' if prefs.get('OperatorUser') == getpass.getuser()
                                      else 'denied_likely')
                if out['serve_write'] == 'denied_likely':
                    out['serve_write_fix'] = OPERATOR_FIX
            except Failure as exc:
                out['warnings'].append(str(exc))
    try:
        config = serve_status()
        # Ports served on this machine's own name; Services have names of their own.
        out['machine_ports'] = sorted(machine_ports(config))
        if ports:
            pool = ports_value(ports)
            occupied = config_ports(config)
            out['free_ports'] = [port for port in pool if port not in occupied]
    except Failure as exc:
        out['warnings'].append(str(exc))
    return out


def machine_ports(config):
    """Ports served on the machine name; browsers send its cookies to all of them."""
    ports = {port_key(key) for key in (config.get('TCP') or {})}
    for session in (config.get('Foreground') or {}).values():
        ports.update(port_key(key) for key in (session.get('TCP') or {}))
    return ports


def mounts(config, record):
    hp = record['fqdn'] + ':' + str(record['port'])
    return config.get('Web', {}).get(hp, {}).get('Handlers', {})


def private_slot(config, record, allowed):
    """Other scopes and hostnames must not claim the selected port."""
    port, hp = record['port'], record['fqdn'] + ':' + str(record['port'])
    other = {k: v for k, v in config.items() if k not in ('TCP', 'Web')}
    require(port not in config_ports(other), 'Selected port has a Funnel, foreground or Service conflict')
    for key in config.get('Web', {}):
        require(host_port(key) != port or key == hp, 'Selected port belongs to another hostname')
    tcp = config.get('TCP', {}).get(str(port))
    require(tcp is None or tcp == {'HTTPS': True}, 'Selected listener changed or is not HTTPS')
    actual = mounts(config, record)
    require(all(path in allowed and handler == allowed[path] for path, handler in actual.items()),
            'Selected port has changed or foreign mounts; leaving them untouched')
    return actual


class Store:
    """Keep private, atomic ownership records under a cooperating-process lock."""
    def __init__(self):
        root = os.environ.get('XDG_STATE_HOME')
        self.directory = (Path(root) if root else Path.home() / '.local/state') / 'tailnet-preview'
        require(self.directory.is_absolute(), 'XDG_STATE_HOME must be absolute')

    @contextlib.contextmanager
    def lock(self):
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        require(not self.directory.is_symlink() and self.directory.stat().st_uid == os.getuid(), 'State directory must be owned and not a symlink')
        os.chmod(self.directory, 0o700)
        fd = os.open(self.directory / 'lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            os.fchmod(fd, 0o600)
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                # Another helper command holds the lock; say so instead of hanging silently.
                print('Waiting for another tailnet-preview command on this account to finish...', file=sys.stderr, flush=True)
                fcntl.flock(fd, fcntl.LOCK_EX)
            yield
        finally:
            os.close(fd)

    def path(self, preview_id):
        require(re.fullmatch('[a-zA-Z0-9_-]{1,64}', preview_id), 'ID must contain 1 to 64 ASCII letters, digits, underscores or hyphens')
        return self.directory / (preview_id + '.json')

    def load(self, preview_id):
        path = self.path(preview_id)
        require(not path.is_symlink(), 'State record must not be a symlink')
        require(path.exists(), 'No owned preview record for ' + preview_id)
        return decode(path.read_bytes())

    def save(self, record):
        fd, name = tempfile.mkstemp(prefix='.state-', dir=self.directory)
        try:
            with os.fdopen(fd, 'w', encoding='utf-8') as stream:
                json.dump(record, stream, ensure_ascii=False)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(name, self.path(record['id']))
        finally:
            if os.path.exists(name):
                os.unlink(name)


def listed_record(record):
    """Add card metadata and local app state while preserving recovery fields."""
    item = dict(record)
    metadata = {key: None for key in ('title', 'description', 'set', 'version')}
    try:
        card = card_value(record['card'])
        metadata.update({key: card.get(key) for key in metadata})
    except (Failure, KeyError, TypeError, UnicodeError) as exc:
        item['warnings'] = list(item.get('warnings', [])) + ['Malformed recorded card: ' + str(exc)]
    item.update(metadata)
    item['app'] = app_state(record)
    return item


def tailnet_host(host):
    """Match exact node and service DNS names from the shared status snapshot."""
    host = host.rstrip('.').lower()
    data = tailnet_status()
    return (any(node.get('DNSName', '').rstrip('.').lower() == host
                for node in [data.get('Self') or {}] + list((data.get('Peer') or {}).values()))
            or any(record.get('Name', '').rstrip('.').lower() == host
                   for record in data.get('ExtraRecords') or []))


class TailnetStream(io.RawIOBase):
    """Verified TLS over the CLI's userspace-compatible byte stream."""
    def __init__(self, process, host, deadline):
        self.process, self.deadline = process, deadline
        self.incoming, self.outgoing = ssl.MemoryBIO(), ssl.MemoryBIO()
        self.tls = ssl.create_default_context().wrap_bio(
            self.incoming, self.outgoing, server_hostname=host)
        os.set_blocking(process.stdin.fileno(), False)
        os.set_blocking(process.stdout.fileno(), False)
        self.perform(self.tls.do_handshake)

    def ready(self, pipe, writing=False):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError('request deadline exceeded')
        readers, writers, _ = select.select(
            [] if writing else [pipe], [pipe] if writing else [], [], remaining)
        if not readers and not writers:
            raise TimeoutError('request deadline exceeded')

    def flush_tls(self):
        while self.outgoing.pending:
            data = memoryview(self.outgoing.read())
            while data:
                self.ready(self.process.stdin, writing=True)
                count = os.write(self.process.stdin.fileno(), data)
                data = data[count:]

    def perform(self, operation):
        while True:
            if time.monotonic() >= self.deadline:
                raise TimeoutError('request deadline exceeded')
            try:
                result = operation()
                self.flush_tls()
                return result
            except ssl.SSLWantWriteError:
                self.flush_tls()
            except ssl.SSLWantReadError:
                self.flush_tls()
                self.ready(self.process.stdout)
                data = os.read(self.process.stdout.fileno(), 65536)
                if data:
                    self.incoming.write(data)
                else:
                    self.incoming.write_eof()

    def send(self, data):
        while data:
            count = self.perform(lambda: self.tls.write(data))
            data = data[count:]

    def readable(self):
        return True

    def readinto(self, buffer):
        data = self.perform(lambda: self.tls.read(len(buffer)))
        buffer[:len(data)] = data
        return len(data)

    def makefile(self, mode):
        return io.BufferedReader(self)


def http_failure(code, url, location):
    return Failure('HTTP ' + str(code) + ' at ' + url
                   + ('; redirect not followed: ' + location if location else ''))


def body(response, limit, prefix, url):
    data = response.read(limit if prefix else limit + 1)
    require(prefix or len(data) <= limit, 'Response exceeds size limit at ' + url)
    return response.status, data


def tailnet_request(url, parsed, method, limit, prefix, deadline):
    process = subprocess.Popen(cli_argv(['nc', parsed.hostname, str(parsed.port or 443)]),
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, env=cli_env(), bufsize=0)
    try:
        stream = TailnetStream(process, parsed.hostname, deadline)
        path = urllib.parse.urlunsplit(('', '', parsed.path or '/', parsed.query, ''))
        headers = (method + ' ' + path + ' HTTP/1.1\r\nHost: ' + parsed.netloc
                   + '\r\nUser-Agent: tailnet-preview\r\nConnection: close\r\n'
                   + ('Content-Length: 0\r\n' if method == 'POST' else '') + '\r\n')
        stream.send(headers.encode('ascii'))
        with http.client.HTTPResponse(stream) as response:
            response.begin()
            if response.status >= 300:
                raise http_failure(response.status, url, response.getheader('Location', ''))
            return body(response, limit, prefix, url)
    finally:
        process.kill()
        process.wait()
        process.stdin.close()
        process.stdout.close()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


def request(url, method='GET', limit=MAX_BODY, prefix=False, timeout=TIMEOUT):
    """Bound response reads; app probes need only a prefix, not the whole page."""
    deadline = time.monotonic() + timeout
    try:
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme == 'https' and tailnet_host(parsed.hostname):
            return tailnet_request(url, parsed, method, limit, prefix, deadline)
        with OPENER.open(urllib.request.Request(url, method=method), timeout=timeout) as response:
            return body(response, limit, prefix, url)
    except urllib.error.HTTPError as exc:
        location = exc.headers.get('Location', '')
        exc.close()
        raise http_failure(exc.code, url, location) from exc
    except (OSError, urllib.error.URLError, http.client.HTTPException) as exc:
        raise Failure('Request failed at ' + url + ': ' + str(exc)) from exc


def expected_app(url, record):
    code, body = request(url, prefix=True)
    require(code == record['expected_status'], 'Unexpected app HTTP status at ' + url)
    if record['expect_text']:
        require(record['expect_text'].encode('utf-8') in body, 'Expected app text missing at ' + url)


def app_state(record):
    """Compare the local app with its recorded status and text, if published."""
    if record.get('phase') not in ('mounted', 'verified') or not record.get('upstream'):
        return None
    try:
        upstream, route = record['upstream'], record.get('route', '/')
        text = record.get('expect_text')
        require(isinstance(text, str), 'Invalid recorded expected app text')
        require(isinstance(upstream, str) and isinstance(route, str), 'Invalid recorded app address')
        upstream = upstream_value(upstream)
        require(route.startswith('/') and not route.startswith('//') and not any(c in route for c in '\r\n?#'),
                'Route must be an absolute local path without query or fragment')
    except (Failure, TypeError, ValueError):
        return None
    try:
        code, body = request(upstream + route, prefix=True, timeout=APP_CHECK_TIMEOUT)
    except Failure as exc:
        cause = exc.__cause__
        if isinstance(cause, (urllib.error.HTTPError, http.client.HTTPException)):
            return 'different'
        if isinstance(getattr(cause, 'reason', cause), socket.timeout):
            return None  # slow, for example while a dev server recompiles
        return 'stopped'
    if code != record.get('expected_status') or (text and text.encode('utf-8') not in body):
        return 'different'
    return 'ok'


def external_card(url):
    """The first HTTPS handshake can wait for certificate issuance."""
    deadline = time.monotonic() + 90
    while True:
        try:
            return request(url, limit=4096, timeout=min(TIMEOUT, deadline - time.monotonic()))
        except Failure as exc:
            remaining = deadline - time.monotonic()
            if not str(exc).startswith('Request failed at ') or remaining <= 0:
                raise
            time.sleep(min(3, remaining))
            if time.monotonic() >= deadline:
                raise


def ownership(record, current):
    require(all(record[k] == current[k] for k in ('node', 'fqdn')), 'Current Tailscale node/FQDN differs from ownership record; use the original authorized host')


def result(record):
    return {'id': record['id'], 'url': record['url'], 'published': False, 'app_verified': False,
            'card_verified': False, 'listed': None, 'warnings': []}


def probe_warning(stage, url, transport, started, error):
    return ('Verification failed: stage=' + stage + '; route=' + url
            + '; transport=' + transport + '; elapsed_ms='
            + str(round((time.monotonic() - started) * 1000)) + '; error=' + str(error))


def verify(record, current):
    """Keep observed publication and verification facts even when a check fails."""
    out = result(record)
    stage, url, transport = 'ownership/private mounts', record['url'], 'tailscale CLI'
    started = time.monotonic()
    try:
        ownership(record, current)
        allowed = {'/': {'Proxy': record['upstream']}, CARD_PATH: {'Text': record['card']}}
        actual = private_slot(serve_status(), record, allowed)
        require(actual == allowed, 'Expected root/card mounts missing')
        out['published'] = True
        stage, url, transport = 'loopback app', record['upstream'] + record['route'], 'direct HTTP'
        started = time.monotonic()
        expected_app(url, record)
        stage, url, transport = 'HTTPS card', record['url'].rstrip('/') + CARD_PATH, 'tailscale nc / certificate-checked TLS'
        started = time.monotonic()
        code, body = external_card(url)
        require(code == 200 and card_value(body) == card_value(record['card'].encode()), 'Published card differs from expected card')
        out['card_verified'] = True
        stage, url = 'HTTPS app', record['url'].rstrip('/') + record['route']
        started = time.monotonic()
        expected_app(url, record)
        out['app_verified'] = True
        return out
    except Failure as exc:
        out['warnings'].append(probe_warning(stage, url, transport, started, exc))
        raise PublicationFailure(out) from exc


def catalog_time(text):
    """Parse Go RFC3339Nano timestamps on Python 3.9 without rounding forward."""
    match = re.fullmatch(r'(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})', text)
    require(match, 'Catalog has no valid updated timestamp')
    base, fraction, zone = match.groups()
    stamp = base + '.' + (fraction or '').ljust(6, '0')[:6] + zone.replace('Z', '+00:00')
    return datetime.datetime.fromisoformat(stamp).timestamp()


def home_confirm(home, record, out):
    """Confirm indexing independently of the local ownership lock."""
    if not home:
        return
    out['listed'] = False
    try:
        p = urllib.parse.urlsplit(home)
        require(p.scheme == 'https' and p.hostname and not p.username and not p.password
                and p.path in ('', '/') and not p.query and not p.fragment,
                'Home must be a trusted canonical HTTPS origin')
        p.port
    except (Failure, ValueError) as exc:
        out['warnings'].append('Published; home listing not confirmed. ' + str(exc))
        return
    home = home.rstrip('/')
    last_error = 'Catalog remained stale or metadata did not match'
    for attempt in range(3):
        if attempt:
            time.sleep(11)
        try:
            # 303 is expected, but never follow it or treat it as a receipt.
            try:
                request(home + '/refresh', method='POST')
            except Failure as exc:
                # A slow synchronous refresh can time out; the catalog still decides.
                if not str(exc).startswith(('HTTP 303 ', 'Request failed at ')):
                    raise
            _, services_body = request(home + '/api/services.json')
            _, list_body = request(home + '/api/list.json')
            services, entries = decode(services_body), decode(list_body)
            require(isinstance(services, dict) and isinstance(services.get('updated'), str),
                    'Catalog has no valid updated timestamp')
            updated = catalog_time(services['updated'])
            card = card_value(record['card'].encode())
            fresh = updated >= record['attempted_at'] and not services.get('error')
            match = any(isinstance(e, dict) and e.get('url') == record['url']
                        and ('title' not in card or e.get('label') == card['title'])
                        and all(e.get(k, '') == card.get(k, '') for k in
                                ('description', 'set', 'version'))
                        for e in entries) if isinstance(entries, list) else False
            if fresh and match:
                out['listed'] = True
                return
        except (Failure, KeyError, TypeError, ValueError) as exc:
            last_error = str(exc)
    out['warnings'].append('Published; home listing not confirmed. ' + last_error)


def remove_owned(store, record, dry_run=False):
    """Remove only recorded, unchanged mounts; retain conflicts for safe retries."""
    ownership(record, identity())
    if record['phase'] == 'intent':
        # No write was attempted, so even an identical mapping cannot be ours.
        if dry_run:
            record['dry_run_commands'] = ['discard ownership record; no mounts were written']
        else:
            store.path(record['id']).unlink()
        return []
    conflicts = []
    for path, expected in [(CARD_PATH, {'Text': record['card']}), ('/', {'Proxy': record['upstream']})]:
        # Inspect each time; a human may have edited the other mount since the last command.
        config = serve_status()
        hp = record['fqdn'] + ':' + str(record['port'])
        other = {k: v for k, v in config.items() if k not in ('TCP', 'Web')}
        if (record['port'] in config_ports(other)
                or any(key != hp and host_port(key) == record['port'] for key in config.get('Web', {}))):
            conflicts.append('Exposure/scope conflict on port ' + str(record['port']))
            break
        actual = mounts(config, record).get(path)
        if actual is None:
            record['removed'][path] = True
            if not dry_run:
                store.save(record)
            continue
        if actual != expected or config.get('TCP', {}).get(str(record['port'])) != {'HTTPS': True}:
            conflicts.append('Changed mount ' + hp + path + '; left untouched')
            continue
        args = ['serve', '--https=' + str(record['port']), '--set-path=' + path, 'off']
        if dry_run:
            record.setdefault('dry_run_commands', []).append(cli_argv(args))
            continue
        try:
            run(args)
            require(path not in mounts(serve_status(), record), 'Mount still exists after removal: ' + path)
            record['removed'][path] = True
            store.save(record)
        except Failure as exc:
            conflicts.append(str(exc))
    if not dry_run and all(record['removed'].get(p) for p in (CARD_PATH, '/')):
        store.path(record['id']).unlink()
    return conflicts


def cleanup(args, store):
    """Snapshot local members under the caller's lock, then remove independently."""
    name = set_value(args.set) if args.set is not None else None
    records = set_records(store, name) if name is not None else [store.load(args.id)]
    results = []
    for record in records:
        warnings = []
        try:
            warnings = remove_owned(store, record, args.dry_run)
        except (Failure, OSError, KeyError, TypeError, UnicodeError) as exc:
            warnings.append(str(exc))
        item = {'id': record.get('id'), 'removed': record.get('removed', {}),
                'warnings': warnings, 'dry_run': args.dry_run}
        if args.dry_run:
            item['commands'] = record.get('dry_run_commands', [])
        results.append(item)
    out = {'selected': len(records), 'results': results, 'dry_run': args.dry_run,
           'noop': not records}
    if name is not None:
        out['set'] = name
    else:
        # Preserve the existing single-ID fields for callers.
        out.update(results[0])
    return out, any(item['warnings'] for item in results)


def load_card(args):
    if args.card == '-':
        raw = sys.stdin.buffer.read(4097)
    else:
        path = Path(args.card)
        require(path.is_absolute(), 'Card file path must be absolute, or use - for stdin')
        with path.open('rb') as stream:
            raw = stream.read(4097)
    card = card_value(raw)
    card['preview'] = True
    text = json.dumps(card, ensure_ascii=False, separators=(',', ':'))
    require(len(text.encode()) <= 4096, 'Encoded card exceeds 4096 bytes')
    return text


def publish(args, store):
    """Write recoverable ownership before mounting; compensate only owned paths."""
    ports = ports_value(args.ports)
    current = identity()
    upstream = upstream_value(args.upstream)
    require(args.route.startswith('/') and not args.route.startswith('//') and not any(c in args.route for c in '\r\n?#'), 'Route must be an absolute local path without query or fragment')
    require(200 <= args.expected_status <= 299, 'Expected app status must be 2xx')
    text = load_card(args)
    require(not store.path(args.id).exists(), 'Preview ID already exists; use status or cleanup, not overwrite')
    occupied = config_ports(serve_status())
    free = [port for port in ports if port not in occupied]
    require(free, 'Approved port pool is full or conflicted; request explicit cleanup, never evict')
    port = free[0]
    url = 'https://' + current['fqdn'] + (':' + str(port) if port != 443 else '') + '/'
    record = dict(current, id=args.id, port=port, url=url, upstream=upstream, card=text,
                  route=args.route, expected_status=args.expected_status, expect_text=args.expect_text,
                  attempted_at=time.time(), phase='intent', removed={})
    commands = [['serve', '--bg', '--https=' + str(port), '--set-path=/', upstream],
                ['serve', '--bg', '--https=' + str(port), '--set-path=' + CARD_PATH, 'text:' + text]]
    if args.dry_run:
        return dict(result(record), dry_run=True, commands=[cli_argv(c) for c in commands]), None
    started = time.monotonic()
    try:
        expected_app(upstream + args.route, record)
    except Failure as exc:
        out = result(record)
        out.update(compensation_completed=True, ownership_record_remains=False)
        out['warnings'] = [
            probe_warning('loopback app', upstream + args.route, 'direct HTTP', started, exc),
            'Compensation completed: true (no mounts written); ownership record remains: false']
        raise PublicationFailure(out) from exc
    store.save(record)
    try:
        require(port not in config_ports(serve_status()), 'Port was claimed before root publication')
        record['phase'] = 'writing-root'
        store.save(record)
        run(commands[0])
        record['phase'] = 'root'
        store.save(record)
        actual = private_slot(serve_status(), record, {'/': {'Proxy': upstream}})
        require(actual == {'/': {'Proxy': upstream}}, 'Root publication did not match expected mapping')
        run(commands[1])
        record['phase'] = 'mounted'
        store.save(record)
        out = verify(record, current)
        record['phase'] = 'verified'
        store.save(record)
    except Failure as exc:
        out = exc.output if isinstance(exc, PublicationFailure) else result(record)
        if not isinstance(exc, PublicationFailure):
            out['warnings'].append(str(exc))
        try:
            if record['phase'] == 'intent':
                store.path(record['id']).unlink()
                conflicts = []
            else:
                conflicts = remove_owned(store, record)
        except (Failure, OSError) as cleanup_error:
            conflicts = ['Compensation failed: ' + str(cleanup_error)]
        out['warnings'].extend(conflicts)
        out['ownership_record_remains'] = store.path(record['id']).exists()
        out['compensation_completed'] = not conflicts and not out['ownership_record_remains']
        out['published'] = False
        out['warnings'].append(
            'Compensation completed: ' + str(out['compensation_completed']).lower()
            + '; ownership record remains: ' + str(out['ownership_record_remains']).lower())
        raise PublicationFailure(out) from exc
    return out, record


class PublicationFailure(Failure):
    def __init__(self, output):
        message = output['warnings'][0] if output.get('warnings') else 'Preview operation failed'
        super().__init__(message)
        output['error'] = message
        self.output = output


def build_parser():
    cli_help = "Set TAILSCALE to an executable path when the CLI is not on the agent's PATH. On macOS the helper forces CLI mode with TAILSCALE_BE_CLI=1."
    parser = argparse.ArgumentParser(description=__doc__ + ' ' + cli_help)
    subs = parser.add_subparsers(dest='command', required=True)
    pre = subs.add_parser('preflight', help='Read-only prerequisite, machine-port and optional free-port inspection')
    pre.add_argument('--ports', default=os.environ.get('TAILNET_PREVIEW_PORTS', ''),
                     help='Optional port pool to inspect; inspection does not approve publication')
    id_help = "Unique ID in this account's local ownership state: 1-64 ASCII letters, digits, hyphens or underscores."
    home_help = 'Optional trusted full HTTPS tailhome URL; defaults to TAILHOME_URL. Omit for direct-link-only previews. Listing does not prove viewer access.'
    pub = subs.add_parser('publish', help='Publish an app and preview card in a free approved slot', description=cli_help)
    pub.add_argument('--id', required=True, help=id_help)
    pub.add_argument('--upstream', required=True, help="Existing app on this machine's literal loopback HTTP address with an explicit port, for example http://127.0.0.1:3000. Does not start the app.")
    pub.add_argument('--card', required=True, help='Absolute JSON file path, or - for stdin')
    pub.add_argument('--ports', default=os.environ.get('TAILNET_PREVIEW_PORTS', ''), help='User-approved HTTPS ports or comma-separated ranges, at most 32. Defaults to TAILNET_PREVIEW_PORTS; required if unset. No built-in pool. Port 443 needs explicit approval. Does not configure tailhome discovery.')
    pub.add_argument('--home', default=os.environ.get('TAILHOME_URL', ''), help=home_help)
    pub.add_argument('--route', default='/', help='App route to verify on both loopback and the exact HTTPS origin; defaults to /.')
    pub.add_argument('--expected-status', type=int, default=200, help='Expected successful HTTP status for app verification; defaults to 200.')
    pub.add_argument('--expect-text', default='', help='Identifying text required in the first 1 MiB of the app response; defaults to no text check.')
    pub.add_argument('--dry-run', action='store_true', help='Inspect planned Serve commands without publishing; still reads local and Tailscale state.')
    status = subs.add_parser('status', aliases=['check'], help='Verify one owned app, card and private mapping', description=cli_help)
    status.add_argument('--id', required=True, help=id_help)
    status.add_argument('--home', default=os.environ.get('TAILHOME_URL', ''), help=home_help)
    clean = subs.add_parser('cleanup', help='Remove only unchanged recorded card and root mounts', description=cli_help)
    selectors = clean.add_mutually_exclusive_group(required=True)
    selectors.add_argument('--id', help=id_help)
    selectors.add_argument('--set', help='Exact locally owned comparison set')
    clean.add_argument('--dry-run', action='store_true', help='Inspect exact owned-mount removals without changing Serve or ownership records; still reads state.')
    listing = subs.add_parser('list', help='List local ownership records, not discovered or third-party previews')
    listing.add_argument('--set', help='Exact locally owned comparison set')
    return parser


def main(argv=None):
    tailnet_status.cache_clear()
    args = build_parser().parse_args(argv)
    try:
        if args.command == 'preflight':
            out = preflight(args.ports)
            print(json.dumps(out, ensure_ascii=False))
            return 1 if out['blockers'] else 0
        store = Store()
        record = None
        failure = None
        with store.lock():
            if args.command == 'publish':
                out, record = publish(args, store)
            elif args.command == 'list':
                if args.set is None:
                    records = [store.load(p.stem) for p in sorted(store.directory.glob('*.json'))]
                else:
                    name = set_value(args.set)
                    records = set_records(store, name)
            elif args.command in ('status', 'check'):
                record = store.load(args.id)
                record['attempted_at'] = time.time()
                try:
                    out = verify(record, identity())
                except PublicationFailure as exc:
                    failure, out = exc, exc.output
                    if args.home:
                        out['listed'] = False
                except Failure as exc:
                    out = result(record)
                    out['warnings'].append(str(exc))
                    raise PublicationFailure(out) from exc
            else:
                out, conflicts = cleanup(args, store)
                if conflicts:
                    print(json.dumps(out, ensure_ascii=False))
                    return 1
        if args.command == 'list':
            out = {'previews': [listed_record(item) for item in records]}
        if record is not None and (failure is None or out['published']):
            # Listing is independent of app health; confirm it whenever the mounts exist.
            home_confirm(args.home, record, out)
        if failure is not None:
            print(str(failure), file=sys.stderr)
        print(json.dumps(out, ensure_ascii=False))
        return 1 if failure is not None else 0
    except (Failure, OSError, UnicodeError) as exc:
        output = getattr(exc, 'output', {'error': str(exc)})
        if getattr(args, 'home', '') and output.get('listed', False) is None:
            output['listed'] = False
        print(str(exc), file=sys.stderr)
        print(json.dumps(output, ensure_ascii=False))
        return 1


if __name__ == '__main__':
    sys.exit(main())
