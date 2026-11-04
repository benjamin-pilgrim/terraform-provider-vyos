#!/usr/bin/env python3
"""Disposable pinned VyOS acceptance VM; Python standard library only."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
import urllib.request
import uuid

VERSION = '2026.10.22-0026-rolling'
NAME = f'vyos-{VERSION}-generic-amd64.iso'
SHA256 = '438c1139a7e929474e8ca963547c0b10da85569faea4e5e656fc1d1bc3a1e34f'
URL = f'https://github.com/vyos/vyos-nightly-build/releases/download/{VERSION}/{NAME}'
SENTINEL = ['firewall', 'ipv4', 'name', 'TF-CI-SENTINEL']


def verify_image(path):
    with path.open('rb') as source:
        digest = hashlib.file_digest(source, 'sha256').hexdigest()
    if digest != SHA256:
        raise RuntimeError('VyOS ISO checksum mismatch')


def fetch(cache):
    cache.mkdir(parents=True, exist_ok=True)
    image = cache / NAME
    if image.exists():
        verify_image(image)
        return image
    temporary = image.with_suffix('.part')
    try:
        with urllib.request.urlopen(URL, timeout=60) as source, temporary.open('wb') as target:
            while block := source.read(1024 * 1024):
                target.write(block)
        verify_image(temporary)
        temporary.replace(image)
    finally:
        temporary.unlink(missing_ok=True)
    return image


def qemu_command(image, boot, seed, state, port, accelerator):
    # QEMU restrict=on blocks guest traffic to the host and outside networks.
    # Only the explicit HTTPS forward is allowed, bound to host loopback.
    return [
        'qemu-system-x86_64', '-accel', accelerator,
        '-cpu', 'host' if accelerator == 'kvm' else 'max',
        '-smp', '2', '-m', '1536', '-display', 'none', '-nodefaults',
        '-no-reboot', '-device', 'virtio-rng-pci',
        '-kernel', str(boot / 'vmlinuz'), '-initrd', str(boot / 'initrd.img'),
        '-append', 'boot=live components hostname=vyos username=live nopersistence '
        'noautologin nonetworking union=overlay console=ttyS0,115200 '
        'net.ifnames=0 biosdevname=0',
        '-drive', f'file={image},media=cdrom,readonly=on,if=ide',
        '-drive', f'file={seed},media=cdrom,readonly=on,if=ide',
        '-serial', f'unix:{state / "serial.sock"},server=on,wait=off',
        '-netdev', f'user,id=api,restrict=on,ipv6=off,hostfwd=tcp:127.0.0.1:{port}-10.0.2.15:443',
        '-device', 'virtio-net-pci,netdev=api,mac=02:00:00:00:00:01',
    ]


class Console:
    def __init__(self, path, process, log):
        self.process = process
        self.log = log
        self.buffer = ''
        self.condition = threading.Condition()
        self.lock = threading.Lock()
        self.socket = socket.socket(socket.AF_UNIX)
        deadline = time.monotonic() + 30
        while True:
            try:
                self.socket.connect(str(path))
                break
            except (FileNotFoundError, ConnectionRefusedError):
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('QEMU serial socket unavailable; see qemu.log')
                time.sleep(.1)
        self.reader = threading.Thread(target=self.read, daemon=True)
        self.reader.start()

    def read(self):
        try:
            while chunk := self.socket.recv(65536):
                text = chunk.decode(errors='replace')
                self.log.write(text)
                self.log.flush()
                with self.condition:
                    self.buffer += text
                    self.condition.notify_all()
        except (OSError, ValueError):
            pass

    def expect(self, pattern, start=0, timeout=600):
        deadline = time.monotonic() + timeout
        with self.condition:
            while True:
                match = re.search(pattern, self.buffer[start:])
                if match:
                    return start + match.end()
                if self.process.poll() is not None:
                    raise RuntimeError('VM exited; see qemu.log and serial.log')
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('VM console timed out; see serial.log')
                self.condition.wait(min(remaining, 1))

    def login(self):
        pos = self.expect(r'login:')
        self.socket.sendall(b'vyos\n')
        pos = self.expect(r'Password:', pos, 30)
        self.socket.sendall(b'vyos\n')
        self.expect(r'vyos@[^\r\n]*[\$#] ', pos, 60)
        self.socket.sendall(b'sudo -s\n')
        self.expect(r'root@[^\r\n]*# ', pos, 60)
        self.command('stty -echo')

    def command(self, command, timeout=240):
        with self.lock:
            token = uuid.uuid4().hex
            start = len(self.buffer)
            script = ('export SYSTEMD_PAGER=cat PAGER=cat TERM=dumb; '
                      f'printf "\\n__START_{token}__\\n"; (' + command +
                      f') 2>&1; result=$?; printf "\\n__DONE_{token}__:%s\\n" "$result"')
            self.socket.sendall(('bash -c ' + shlex.quote(script) + '\n').encode())
            output_start = self.expect('__START_' + token + r'__\r?\n', start, timeout)
            end = self.expect('__DONE_' + token + r'__:\d+\r?\n', output_start, timeout)
            text = self.buffer[output_start:end]
            match = re.search('__DONE_' + token + r'__:(\d+)', text)
            if int(match.group(1)):
                raise RuntimeError('VM command failed: ' + text[:match.start()].strip())
            return text[:match.start()].strip()

    def close(self):
        self.socket.close()
        self.reader.join(timeout=5)


def native_script(path, expected):
    # Full configuration stays in guest memory; only the assertion result leaves.
    return '''import json,subprocess
from vyos.configtree import ConfigTree
path=json.loads(%r)
expected=json.loads(%r)
active=ConfigTree(subprocess.check_output(['cli-shell-api','showConfig','--show-active-only'],text=True))
saved=ConfigTree(open('/config/config.boot').read())
for source,tree in [('active',active),('saved',saved)]:
 exists=tree.exists(path)
 if expected is None:
  assert not exists,(source,path,'unexpected presence')
 else:
  assert exists,(source,path,'missing configuration')
  node=json.loads(tree.to_json())
  for part in path:node=node[part]
  assert node==expected,(source,path,'value differs')
print('PASS native active/saved check')
''' % (json.dumps(path), json.dumps(expected))


class Checks:
    def __init__(self, console, state):
        self.console = console
        self.records = []
        self.path = state / 'checks.sock'
        self.server = socket.socket(socket.AF_UNIX)
        self.server.bind(str(self.path))
        self.server.listen()
        self.thread = threading.Thread(target=self.serve, daemon=True)
        self.thread.start()

    def check(self, path, value):
        result = self.console.command('python3 -c ' + shlex.quote(native_script(path, value)))
        self.records.append({'path': path, 'passed': True})
        return result

    def serve(self):
        while True:
            try:
                connection, _ = self.server.accept()
            except OSError:
                return
            with connection:
                try:
                    with connection.makefile('rb') as stream:
                        request = json.loads(stream.readline(1024 * 1024))
                    result = self.check(request['path'].split(' '), json.loads(request['value']))
                    response = {'ok': True, 'message': result}
                except Exception as error:
                    response = {'ok': False, 'message': str(error)}
                try:
                    connection.sendall(json.dumps(response).encode())
                except OSError:
                    pass

    def close(self):
        try:
            self.server.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        self.server.close()
        self.thread.join(timeout=5)


def check_client(path, value):
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(300)
        client.connect(os.environ['VYOS_CI_SOCKET'])
        client.sendall((json.dumps({'path': path, 'value': value}) + '\n').encode())
        client.shutdown(socket.SHUT_WR)
        data = bytearray()
        while block := client.recv(65536):
            data.extend(block)
    response = json.loads(data)
    if not response['ok']:
        raise RuntimeError(response['message'])
    print(response['message'])


def make_seed(state, key):
    seed = state / 'seed'
    seed.mkdir()
    subprocess.run([
        'openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
        '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1',
        '-keyout', str(seed / 'api.key'), '-out', str(seed / 'api.pem'),
    ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    template = Path(__file__).with_name('vyos-bootstrap.sh').read_text()
    (seed / 'bootstrap.sh').write_text(template.replace('@API_KEY@', key))
    iso = state / 'seed.iso'
    subprocess.run(['xorriso', '-as', 'mkisofs', '-quiet', '-r', '-V', 'VYOSCI', '-o', str(iso), str(seed)],
                   check=True, capture_output=True)
    return iso, seed / 'api.pem'


def api_info(endpoint, certificate):
    context = ssl.create_default_context(cafile=str(certificate))
    # Explicit proxy-free opener; no existing provider endpoint/key is reused.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                        urllib.request.HTTPSHandler(context=context))
    with opener.open(endpoint + '/info?version=true', timeout=10) as response:
        info = json.load(response)
    if not info.get('success') or info['data']['version'] != VERSION:
        raise RuntimeError('API reports a different VyOS build')
    return info['data']['version']


def run(args):
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    image = fetch(args.cache.resolve())
    accelerator = args.accel
    if accelerator == 'auto':
        accelerator = 'kvm' if os.access('/dev/kvm', os.R_OK | os.W_OK) else 'tcg'
    print(f'Verified VyOS {VERSION}; accelerator={accelerator}', flush=True)
    result = {'version': VERSION, 'iso_sha256': SHA256, 'accelerator': accelerator, 'passed': False}
    process = console = checks = tests = None
    with tempfile.TemporaryDirectory(prefix='vyos-ci-') as temporary:
        state = Path(temporary)
        boot = state / 'boot'
        boot.mkdir()
        # Extract into this run's private directory, from the verified ISO.
        for name in ['vmlinuz', 'initrd.img']:
            subprocess.run(['xorriso', '-osirrox', 'on', '-indev', str(image),
                            '-extract', '/live/' + name, str(boot / name)],
                           check=True, capture_output=True)
        key = secrets.token_hex(24)
        seed, certificate = make_seed(state, key)
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            port = reservation.getsockname()[1]
        endpoint = f'https://127.0.0.1:{port}'
        try:
            with (output / 'qemu.log').open('w') as qemu_log, (output / 'serial.log').open('w') as serial_log:
                process = subprocess.Popen(qemu_command(image, boot, seed, state, port, accelerator),
                                           stdout=qemu_log, stderr=subprocess.STDOUT)
                result['vm_pid'] = process.pid
                console = Console(state / 'serial.sock', process, serial_log)
                console.login()
                print('VM booted; configuring test-only HTTPS API', flush=True)
                console.command('mkdir -p /mnt/ci; mount -o ro /dev/disk/by-label/VYOSCI /mnt/ci; '
                                'su - vyos -c "/bin/vbash /mnt/ci/bootstrap.sh"')
                deadline = time.monotonic() + 120
                while True:
                    try:
                        result['api_version'] = api_info(endpoint, certificate)
                        break
                    except (OSError, ValueError):
                        if time.monotonic() > deadline:
                            raise
                        time.sleep(1)
                print('Verified HTTPS and exact API version', flush=True)
                checks = Checks(console, state)
                checks.check(SENTINEL, {'default-action': 'drop'})
                env = os.environ.copy()
                env.update(TF_ACC='1', TF_ACC_TERRAFORM_PATH=str(args.terraform.resolve()),
                           VYOS_ENDPOINT=endpoint, VYOS_API_KEY=key,
                           VYOS_CA_CERTIFICATE_FILE=str(certificate), VYOS_INSECURE='false',
                           VYOS_SKIP_SAVING='false', VYOS_SAVE_FILE='',
                           VYOS_NATIVE_CHECK=str(Path(__file__).resolve()), VYOS_CI_SOCKET=str(checks.path),
                           HTTP_PROXY='', HTTPS_PROXY='', ALL_PROXY='', NO_PROXY='*', CHECKPOINT_DISABLE='1')
                tests = subprocess.Popen([str(args.test_binary.resolve()), '-test.count=1', '-test.v',
                                          '-test.run', 'TestAcc', '-test.timeout', '20m'],
                                         env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, start_new_session=True)
                with (output / 'acceptance.log').open('w') as test_log:
                    def copy_output():
                        try:
                            for line in tests.stdout:
                                print(line, end='', flush=True)
                                test_log.write(line)
                                test_log.flush()
                        except (OSError, ValueError):
                            pass
                    reader = threading.Thread(target=copy_output, daemon=True)
                    reader.start()
                    result['test_exit_code'] = tests.wait(timeout=21 * 60)
                    reader.join(timeout=10)
                    if reader.is_alive():
                        raise RuntimeError('Acceptance output did not close')
                if result['test_exit_code'] != 0:
                    raise RuntimeError('Acceptance tests failed; see acceptance.log')
                for path in [['firewall', 'ipv4', 'name', 'TF-PROVIDER-TEST'],
                             ['service', 'ssh', 'disable-host-validation']]:
                    checks.check(path, None)
                checks.check(SENTINEL, {'default-action': 'drop'})
                print('PASS native destroy cleanup and unrelated sentinel', flush=True)
                result['native_checks'] = checks.records
                result['passed'] = True
        finally:
            if tests is not None:
                # Kill only this test process group, including Terraform children.
                try:
                    os.killpg(tests.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    tests.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(tests.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    tests.wait()
            if checks is not None:
                result['native_checks'] = checks.records
                checks.close()
            if process is not None and process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            if console is not None:
                console.close()
            result['vm_stopped'] = process is None or process.poll() is not None
            (output / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print('PASS disposable VM acceptance; VM stopped and temporary seed removed', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='action', required=True)
    download = commands.add_parser('fetch')
    download.add_argument('--cache', type=Path, default=Path('.ci-cache'))
    execute = commands.add_parser('run')
    execute.add_argument('--cache', type=Path, default=Path('.ci-cache'))
    execute.add_argument('--output', type=Path, default=Path('.ci-results'))
    execute.add_argument('--accel', choices=['auto', 'kvm', 'tcg'], default='auto')
    execute.add_argument('--test-binary', type=Path, required=True)
    execute.add_argument('--terraform', type=Path, required=True)
    check = commands.add_parser('check')
    check.add_argument('path')
    check.add_argument('value')
    args = parser.parse_args()
    if args.action == 'fetch':
        print(fetch(args.cache.resolve()))
    elif args.action == 'check':
        check_client(args.path, args.value)
    else:
        def interrupt(_number, _frame):
            raise KeyboardInterrupt
        signal.signal(signal.SIGTERM, interrupt)
        run(args)


if __name__ == '__main__':
    try:
        main()
    except (Exception, KeyboardInterrupt) as error:
        print(f'ERROR: {error}', file=sys.stderr)
        sys.exit(1)
