#!/usr/bin/env python3
"""Exercise main's schema-v0 source -> current provider with the real Terraform CLI.

Only an isolated HTTPS fixture is used. Legacy production source is pinned; its
build uses the current dependency graph so the test isolates schema migration.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import secrets
import shutil
import ssl
import subprocess
import sys
import tarfile
import tempfile
import threading

LEGACY_REF = '53486a373df8f32f751865e3b5eaf0580dd622f9'
LEGACY_BATCH_REF = '7b47778d12ff562c7c3745db6cdf36bea75d83f1'
ROOT = Path(__file__).resolve().parents[1]


def run(command, *, cwd, env=None, expected=0):
    result = subprocess.run(command, cwd=cwd, env=env, capture_output=True, text=True)
    if result.returncode != expected:
        # The legacy provider can echo request data. Keep fixture secrets out of
        # diagnostics; callers can replay this deterministic local scenario.
        raise RuntimeError(f'{command[0]} {command[1]} exited {result.returncode}, expected {expected}')
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--terraform', default=shutil.which('terraform'))
    parser.add_argument('--scenario', choices=['all', 'scalar', 'omitted-flag', 'batch'], default='all')
    args = parser.parse_args()
    if args.scenario == 'all':
        for scenario in ['scalar', 'omitted-flag', 'batch']:
            subprocess.run([sys.executable, __file__, '--terraform', args.terraform,
                            '--scenario', scenario], check=True)
        return
    legacy_ref = LEGACY_REF if args.scenario == 'scalar' else LEGACY_BATCH_REF
    if not args.terraform:
        raise RuntimeError('Terraform CLI is required')
    with tempfile.TemporaryDirectory(prefix='vyos-state-upgrade-') as temporary:
        work = Path(temporary)
        legacy = work / 'legacy-source'
        legacy.mkdir()
        archive = subprocess.run(['git', 'archive', legacy_ref], cwd=ROOT, check=True, capture_output=True)
        (work / 'legacy.tar').write_bytes(archive.stdout)
        with tarfile.open(work / 'legacy.tar') as source:
            source.extractall(legacy, filter='data')
        # Production code/schema come from main. Resolve old imports against the
        # current maintained toolchain/dependencies, retaining the legacy client.
        for name in ['go.mod', 'go.sum']:
            shutil.copy(ROOT / name, legacy / name)
        run(['go', 'mod', 'edit', '-require=github.com/foltik/vyos-client-go@v0.4.2'], cwd=legacy)
        run(['go', 'mod', 'tidy'], cwd=legacy)
        old_binary = work / 'old-provider'
        new_binary = work / 'new-provider'
        run(['go', 'build', '-o', str(old_binary), '.'], cwd=legacy)
        run(['go', 'build', '-o', str(new_binary), '.'], cwd=ROOT)
        cert, key = work / 'cert.pem', work / 'key.pem'
        run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
             '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1',
             '-keyout', str(key), '-out', str(cert)], cwd=work)
        token = secrets.token_hex(24)
        tree = {}
        writes = []

        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                from urllib.parse import parse_qs
                body = self.rfile.read(int(self.headers['Content-Length'])).decode()
                form = parse_qs(body)
                if form.get('key') != [token]:
                    self.send_error(401)
                    return
                payload = json.loads(form['data'][0])
                if self.path == '/configure':
                    writes.append('configure')
                    for command in payload:
                        node = tree
                        for component in command['path'][:-1]:
                            node = node.setdefault(component, {})
                        leaf = command['path'][-1]
                        if command['op'] == 'delete':
                            node.pop(leaf, None)
                        else:
                            node[leaf] = command.get('value', {})
                elif self.path == '/config-file':
                    writes.append('save')
                response = json.dumps({'success': True, 'data': tree if self.path == '/retrieve' else None,
                                       'error': None}).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(response)))
                self.end_headers()
                self.wfile.write(response)

        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            binary_dir = work / 'provider'
            binary_dir.mkdir()
            active = binary_dir / 'terraform-provider-vyos'
            shutil.copy(old_binary, active)
            cli = work / 'terraform.rc'
            cli.write_text('provider_installation {\n dev_overrides { "TGNThump/vyos" = ' + json.dumps(str(binary_dir)) + ' }\n direct {}\n}\n')
            module = work / 'module'
            module.mkdir()
            configuration = {
                'scalar': 'resource "vyos_config" "upgrade" {\n path = "system host-name"\n value = jsonencode("upgrade-fixture")\n}\n',
                'omitted-flag': 'resource "vyos_config" "upgrade" {\n path = "service ssh disable-host-validation"\n}\n',
                'batch': 'resource "vyos_config_batch" "upgrade" {\n entries = { host = { path = ["system", "host-name"], value = jsonencode("upgrade-fixture") } }\n}\n',
            }[args.scenario]
            (module / 'main.tf').write_text('terraform {\n required_providers { vyos = { source = "TGNThump/vyos" } }\n}\n' + configuration)
            env = {**os.environ, 'TF_CLI_CONFIG_FILE': str(cli), 'TF_IN_AUTOMATION': '1',
                   'CHECKPOINT_DISABLE': '1', 'VYOS_ENDPOINT': f'https://127.0.0.1:{server.server_port}',
                   'VYOS_API_KEY': token, 'VYOS_CA_CERTIFICATE_FILE': str(cert), 'VYOS_INSECURE': 'false',
                   'VYOS_SKIP_SAVING': 'false', 'VYOS_SAVE_FILE': ''}
            for name in ['VYOS_AUTH_MODE', 'VYOS_BEARER_TOKEN', 'VYOS_CLIENT_CERTIFICATE_FILE', 'VYOS_CLIENT_KEY_FILE']:
                env.pop(name, None)
            run([args.terraform, 'apply', '-auto-approve', '-input=false'], cwd=module, env=env)
            state_file = module / 'terraform.tfstate'
            before = json.loads(state_file.read_text())['resources'][0]['instances'][0]
            assert before['schema_version'] == 0
            if args.scenario == 'batch':
                assert before['attributes']['entries']['host']['value'] == '"upgrade-fixture"'
            else:
                assert before['attributes']['value'] == ('{}' if args.scenario == 'omitted-flag' else '"upgrade-fixture"')
            writes.clear()
            shutil.copy(new_binary, active)
            plan = subprocess.run([args.terraform, 'plan', '-detailed-exitcode', '-input=false', '-out=upgrade.tfplan'],
                                  cwd=module, env=env, capture_output=True, text=True)
            if args.scenario != 'batch':
                assert plan.returncode == 0, 'unchanged legacy configuration produced a non-no-op plan'
            else:
                assert plan.returncode in (0, 2)
                if plan.returncode == 2:
                    # Dynamic entries can normalize map/list state to object/tuple
                    # representation. Permit only an in-place state normalization,
                    # and assert below that applying it performs zero router writes.
                    shown = run([args.terraform, 'show', '-json', 'upgrade.tfplan'], cwd=module, env=env)
                    changes = json.loads(shown.stdout)['resource_changes']
                    assert len(changes) == 1 and changes[0]['change']['actions'] == ['update']
            run([args.terraform, 'apply', '-input=false', 'upgrade.tfplan'], cwd=module, env=env)
            after = json.loads(state_file.read_text())['resources'][0]['instances'][0]
            assert after['schema_version'] == 1
            assert after['attributes']['id'] == before['attributes']['id']
            run([args.terraform, 'plan', '-detailed-exitcode', '-input=false'], cwd=module, env=env)
            assert writes == [], 'schema upgrade issued router mutations'
            # Confirm that a routine downgrade cannot read upgraded schema state.
            shutil.copy(old_binary, active)
            result = subprocess.run([args.terraform, 'plan', '-input=false'], cwd=module, env=env,
                                    capture_output=True, text=True)
            assert result.returncode != 0 and 'newer provider' in (result.stdout + result.stderr).lower()
            print(f'PASS real Terraform {args.scenario} schema-v0 -> v1: stable identity, zero router writes, final no-op plan; downgrade rejected')
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == '__main__':
    main()
