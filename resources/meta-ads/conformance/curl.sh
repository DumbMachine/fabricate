#!/usr/bin/env bash
set -euo pipefail
mode=${1:-direct}
token=${FAB_META_ADS_TOKEN:?}
opts=(-sS --fail)
if [ "$mode" = direct ]; then
  base="${FAB_META_ADS_URL:?}/v23.0"
elif [ "$mode" = proxy ]; then
  base="https://graph.facebook.com/v23.0"
  opts+=(--noproxy '' --proxy "${HTTPS_PROXY:?}" --cacert "${SSL_CERT_FILE:?}")
else
  exit 2
fi
read_node() { curl "${opts[@]}" -H "Authorization: Bearer $token" "$base/130001"; }
read_node | python3 -c 'import json,sys;assert json.load(sys.stdin)["daily_budget"]=="8000"'
curl "${opts[@]}" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -X POST "$base/130001" --data '{"daily_budget":6400}' | python3 -c 'import json,sys;assert json.load(sys.stdin)["success"] is True'
read_node | python3 -c 'import json,sys;assert json.load(sys.stdin)["daily_budget"]=="6400"'
python3 - "$mode" <<'PY'
import json,os,sys
from datetime import datetime,timezone
mode=sys.argv[1]
entry={'operations':{'read':'passed','write':'passed','persistence':'passed'},'status':'passed'}
path=os.environ.get('FAB_COMPATIBILITY_REPORT')
if path:
    report={'api':'Meta Marketing API v23.0 curated subset','environment':{'label':'Food publisher','manifest':'environments/acme-meta-ads.yaml','messages':3},'integration':'meta-ads','modes':{},'operationLabels':{'read':'Read ad-set budget','write':'Set absolute daily budget','persistence':'Read back stored budget'},'verification':{'kind':'curl','label':'HTTP client','client':os.environ.get('FAB_COMPATIBILITY_CLIENT','curl'),'title':'HTTP API verification'}}
    try:
        with open(path) as f:report=json.load(f)
    except FileNotFoundError:pass
    report['modes'][mode]=entry
    report['testedAt']=datetime.now(timezone.utc).isoformat().replace('+00:00','Z')
    report['testedCommit']=os.environ.get('FAB_COMPATIBILITY_COMMIT','unknown')
    with open(path,'w') as f:json.dump(report,f,indent=2);f.write('\n')
print(json.dumps({'mode':mode,**entry}))
PY
