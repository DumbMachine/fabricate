#!/usr/bin/env python3
"""Exercise the live MCP using a pre-issued project credential.

Uses an isolated synthetic Asana composition as a regression fixture. This is
not a general MCP client: Fabricate is configured to return JSON responses.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--url', required=True, help='Fabricate console origin')
    parser.add_argument('--token-env', default='FABRICATE_MCP_TOKEN')
    parser.add_argument('--work-dir', type=Path, required=True)
    parser.add_argument('--restart-container', help='Optional isolated local test container to SIGKILL/restart')
    parser.add_argument('--keep', action='store_true')
    args = parser.parse_args()
    base = args.url.rstrip('/')
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ('http', 'https') or not parsed.netloc or parsed.username or parsed.query or parsed.fragment:
        parser.error('Provide a console origin without credentials, query or fragment')
    token = os.environ.get(args.token_env, '')
    if not token:
        parser.error('Project credential environment variable is missing')
    os.umask(0o077)
    work = args.work_dir.resolve()
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    work.chmod(0o700)
    if (work / 'receipt.json').exists():
        parser.error('Use a fresh work directory; receipts are never overwritten')
    counter = 0

    def rpc(method, params, notification=False):
        nonlocal counter
        counter += 1
        payload = {'jsonrpc': '2.0', 'method': method, 'params': params}
        if not notification:
            payload['id'] = counter
        req = urllib.request.Request(base + '/mcp', data=json.dumps(payload).encode(), headers={
            'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json',
            'Accept': 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-11-25'})
        with urllib.request.urlopen(req, timeout=60) as response:
            if notification:
                assert response.status == 202
                return None
            result = json.load(response)
        assert 'error' not in result, 'MCP protocol error'
        return result['result']

    def initialize():
        result = rpc('initialize', {'protocolVersion': '2025-11-25', 'capabilities': {},
            'clientInfo': {'name': 'fabricate-live-verifier', 'version': '1'}})
        assert result['protocolVersion'] == '2025-11-25'
        rpc('notifications/initialized', {}, notification=True)
        names = {tool['name'] for tool in rpc('tools/list', {})['tools']}
        assert 'service_read' in names and 'sandbox_create' in names
        assert 'project_token_create' not in names, 'Use a scoped project credential'

    def tool(name, arguments, expected=200, failed=False):
        result = rpc('tools/call', {'name': name, 'arguments': arguments})
        assert bool(result.get('isError')) == failed, f'{name}: unexpected tool failure state'
        structured = result.get('structuredContent', {})
        assert structured.get('status') == expected, f'{name}: unexpected HTTP status'
        return structured.get('data')

    def docker(*arguments):
        return subprocess.check_output(['docker', *arguments], stderr=subprocess.PIPE).decode().strip()

    initialize()
    config = tool('deployment_get', {})
    assert config['durable'] and not config['operator']
    catalog = tool('catalog_list', {})
    assert any(s['id'] == 'asana.acme-sprint.v1' for s in catalog['scenarios'])
    document = tool('scenario_document', {'id': 'asana.acme-sprint.v1'})['document']
    tool('scenario_validate', {'resource': 'asana', 'document': document})
    contract = tool('resource_contract', {'resource': 'asana', 'operations': ['GetTask', 'UpdateTask']})
    paths = {}
    for path, methods in contract['openapi']['paths'].items():
        for method, operation in methods.items():
            if isinstance(operation, dict) and operation.get('operationId'):
                paths[operation['operationId']] = (method.upper(), path)
    task = document['state']['tasks'][0]['gid']
    def provider_path(operation):
        path = paths[operation][1]
        assert len(re.findall(r'\{[^}]+\}', path)) == 1
        return contract['basePath'] + re.sub(r'\{[^}]+\}', task, path)
    key = 'mcp-verifier-' + secrets.token_hex(8)
    creation = {'kind': 'composition', 'id': 'mcp-verification', 'idempotencyKey': key, 'ttlSeconds': 3600,
        'services': {name: {'resource': 'asana', 'scenario': document['$id']} for name in ('primary', 'comparison')}}
    session = tool('sandbox_create', creation, expected=201)
    assert tool('sandbox_create', creation, expected=201)['id'] == session['id']
    tool('sandbox_request_status', {'key': key})
    assert session['kind'] == 'composition'
    original_connections = [(s['name'], s['url'], s['token']) for s in session['services']]
    reference = tool('sandbox_state', {'id': session['id']})['services']['comparison']
    marker = 'Live MCP verification ' + secrets.token_hex(8)
    read = {'id': session['id'], 'service': 'primary', 'path': provider_path('GetTask')}
    tool('service_read', read)
    tool('service_request', {'id': session['id'], 'service': 'primary', 'method': paths['UpdateTask'][0],
        'path': provider_path('UpdateTask'), 'body': {'data': {'notes': marker}}})
    assert tool('service_read', read)['data']['data']['notes'] == marker
    tool('resource_contract', {'resource': 'asana', 'operations': ['unsupported-verifier-operation']}, expected=422, failed=True)
    tool('sandbox_renew', {'id': session['id'], 'ttlSeconds': 7200})

    def verify(stage):
        current = tool('sandbox_get', {'id': session['id']})
        assert [(s['name'], s['url'], s['token']) for s in current['services']] == original_connections
        assert tool('service_read', read)['data']['data']['notes'] == marker
        exported = tool('sandbox_state', {'id': session['id']})['services']
        assert exported['comparison'] == reference, 'Comparison data changed'
        assert tool('sandbox_create', creation, expected=201)['id'] == session['id']
        assert tool('sandbox_request_status', {'key': key})['status'] == 'ready'
        print(stage + ': MCP auth, URLs, tokens, data, comparison and creation-key recovery passed', flush=True)

    verify('before-restart')
    if args.restart_container:
        if parsed.hostname not in ('127.0.0.1', 'localhost', '::1'):
            parser.error('Container restart is allowed only for an isolated local deployment')
        info = json.loads(docker('inspect', args.restart_container))[0]
        env = dict(value.split('=', 1) for value in info['Config']['Env'] if '=' in value)
        assert env.get('FAB_CONSOLE_PUBLIC_URL') == base, 'Container does not match the tested origin'
        docker('kill', '--signal=KILL', args.restart_container)
        assert docker('inspect', '--format', '{{.State.ExitCode}}', args.restart_container) == '137'
        docker('start', args.restart_container)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen(base + '/healthz', timeout=2) as response:
                    if response.status == 200:
                        break
            except (urllib.error.URLError, TimeoutError, ConnectionError):
                pass
            time.sleep(.25)
        else:
            raise RuntimeError('Container did not recover')
        initialize()
        verify('after-SIGKILL')
    assert tool('sandbox_logs', {'id': session['id']})['entries']
    if not args.keep:
        tool('sandbox_request_cancel', {'key': key}, expected=204)
        tool('sandbox_close', {'id': session['id']}, expected=204)
        tool('sandbox_create', creation, expected=410, failed=True)
        tool('sandbox_get', {'id': session['id']}, expected=404, failed=True)
    with (work / 'receipt.json').open('x') as file:
        json.dump({'session': session, 'creationKey': key, 'project': config['project']['id'],
            'restartVerified': bool(args.restart_container), 'closed': not args.keep}, file, indent=2)
    print('Live MCP verification passed; private receipt saved.', flush=True)


if __name__ == '__main__':
    main()
