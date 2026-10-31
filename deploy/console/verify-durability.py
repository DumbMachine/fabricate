#!/usr/bin/env python3
"""Black-box durability regression against an isolated local Docker deployment.

Build the console image first. This creates its own containers and named volumes;
no existing deployment is restarted. --keep retains the final sandbox for review.
All credentials/receipts are written only to the private --work-dir, never stdout.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', default='fabricate-console:durable')
    parser.add_argument('--port', type=int, default=4193)
    parser.add_argument('--work-dir', type=Path, required=True)
    parser.add_argument('--keep', action='store_true')
    args = parser.parse_args()
    work = args.work_dir.resolve()
    work.mkdir(mode=0o700, parents=True, exist_ok=True)
    work.chmod(0o700)
    if any((work / name).exists() for name in ('operator-token', 'run.json', 'receipt.json')):
        parser.error('Use a fresh work directory; existing credentials and receipts are never replaced.')
    token = secrets.token_urlsafe(48)
    secret = work / 'operator-token'
    # Refuse to replace another run's private credential or evidence.
    with secret.open('x') as file:
        file.write(token + '\n')
    secret.chmod(0o444)  # non-root container reads it; host parent is private
    prefix = 'fabricate-durability-' + secrets.token_hex(4)
    state, logs = prefix + '-state', prefix + '-logs'
    base = f'http://127.0.0.1:{args.port}'
    events = []

    def docker(*arguments):
        return subprocess.check_output(['docker', *arguments], stderr=subprocess.PIPE).decode().strip()

    def request(path, method='GET', body=None, credential=token, expected=200):
        url = path if path.startswith('http://') else base + path
        req = urllib.request.Request(url, method=method,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization': 'Bearer ' + credential, 'Content-Type': 'application/json'})
        try:
            with urllib.request.urlopen(req, timeout=30) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read()
        assert code == expected, f'{method} returned {code}, expected {expected}'
        return json.loads(raw) if raw and code < 400 else None

    def healthy():
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen(base + '/healthz', timeout=2) as response:
                    if response.status == 200:
                        return
            except (urllib.error.URLError, TimeoutError, ConnectionError):
                pass
            time.sleep(0.25)
        raise RuntimeError('container did not become ready')

    def launch():
        docker('run', '-d', '--name', prefix, '--init', '--read-only', '--cap-drop=ALL',
            '--security-opt=no-new-privileges', '--tmpfs', '/tmp:rw,nosuid,noexec,size=1g,mode=1777',
            '-p', f'127.0.0.1:{args.port}:4173',
            '-e', 'FAB_STATE_DIR=/var/lib/fabricate/state',
            '-e', 'FAB_CONSOLE_PUBLIC_URL=' + base,
            '-e', 'FAB_CONSOLE_TOKEN_FILE=/run/secrets/operator-token',
            '--mount', f'type=bind,src={secret},dst=/run/secrets/operator-token,readonly',
            '-v', state + ':/var/lib/fabricate/state',
            '-v', logs + ':/var/lib/fabricate/logs', args.image)
        healthy()
        assert request('/api/config')['durable'] is True

    with (work / 'run.json').open('x') as file:
        json.dump({'container': prefix, 'stateVolume': state, 'logVolume': logs, 'baseUrl': base}, file)
    (work / 'run.json').chmod(0o600)

    try:
        launch()
        baseline = request('/api/scenarios/asana.acme-sprint.v1/document')['document']
        task = baseline['state']['tasks'][0]['gid']
        custom = json.loads(json.dumps(baseline['state']))
        custom['tasks'][0]['notes'] = 'custom durable baseline'
        session = request('/api/sessions', 'POST', {
            'kind': 'composition', 'id': 'restart-verification', 'services': {
                'primary': {'resource': 'asana', 'scenario': baseline['$id'], 'state': custom},
                'comparison': {'resource': 'asana', 'scenario': baseline['$id']},
                'mail': {'resource': 'gmail', 'scenario': 'gmail.acme-corp.v1'},
            }}, expected=201)
        services = {service['name']: service for service in session['services']}
        primary, comparison, mail = (services[name] for name in ('primary', 'comparison', 'mail'))
        task_path = '/api/1.0/tasks/' + task
        mutation = 'committed before SIGKILL ' + secrets.token_hex(8)
        request(primary['url'] + task_path, 'PUT', {'data': {'notes': mutation}}, primary['token'])
        mail_before = request(mail['url'] + '/gmail/v1/users/me/messages', credential=mail['token'])
        disposable = request('/api/sessions', 'POST', {'kind': 'scenario', 'id': 'gmail.minimal.v1'}, expected=201)
        request('/api/sessions/' + disposable['id'], 'DELETE', expected=204)

        def verify(stage):
            restored = request('/api/sessions/' + session['id'])
            assert restored['id'] == session['id'] and restored['startedAt'] == session['startedAt']
            actual = {service['name']: service for service in restored['services']}
            for name, original in services.items():
                assert actual[name]['url'] == original['url'], 'URL changed'
                assert actual[name]['token'] == original['token'], 'token changed'
            assert request(primary['url'] + task_path, credential=primary['token'])['data']['notes'] == mutation
            assert request(comparison['url'] + task_path, credential=comparison['token'])['data']['notes'] != mutation
            assert request(mail['url'] + '/gmail/v1/users/me/messages', credential=mail['token']) == mail_before
            request(comparison['url'] + task_path, credential=primary['token'], expected=401)
            request('/api/sessions/' + disposable['id'], expected=404)
            request(disposable['services'][0]['url'] + '/gmail/v1/users/me/messages',
                    credential=disposable['services'][0]['token'], expected=404)
            exported = request('/api/sessions/' + session['id'] + '/state')
            assert any(row['gid'] == task and row['notes'] == mutation for row in exported['services']['primary']['state']['tasks'])
            events.append(stage)
            print(stage + ': URLs, tokens, mutations, isolation, export and deletion verified', flush=True)

        verify('before-kill')
        docker('kill', '--signal=KILL', prefix)
        assert docker('inspect', '--format', '{{.State.ExitCode}}', prefix) == '137'
        docker('start', prefix)
        healthy()
        verify('after-SIGKILL-restart')
        docker('rm', '--force', prefix)
        launch()
        verify('after-container-replacement')
        docker('restart', '--time', '30', prefix)
        healthy()
        verify('after-graceful-restart')
        assert request('/api/sessions/' + session['id'] + '/logs')['entries']
        receipt = {'container': prefix, 'stateVolume': state, 'logVolume': logs,
                   'baseUrl': base, 'session': session, 'events': events}
        path = work / 'receipt.json'
        with path.open('x') as file:
            json.dump(receipt, file, indent=2)
        path.chmod(0o600)
        print('Durability verification passed; private receipt saved in work directory.', flush=True)
        if args.keep:
            print('Preview: ' + base + '/sandboxes/' + session['id'], flush=True)
    finally:
        if not args.keep:
            subprocess.run(['docker', 'rm', '--force', prefix], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            subprocess.run(['docker', 'volume', 'rm', state, logs], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            secret.unlink(missing_ok=True)


if __name__ == '__main__':
    main()
