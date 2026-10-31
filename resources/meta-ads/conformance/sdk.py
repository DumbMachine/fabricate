"""Official Meta SDK smoke: a read, absolute budget update and independent read.

Run in a disposable Fabricate session. Requires facebook-business==26.0.2.
"""
import json
import os
import sys
from facebook_business.session import FacebookSession
from facebook_business.api import FacebookAdsApi
from facebook_business.adobjects.adset import AdSet
from facebook_business.adobjects.adaccount import AdAccount

mode=sys.argv[1] if len(sys.argv)>1 else 'direct'
if mode not in {'direct','proxy'}:
    raise SystemExit('expected direct or proxy')
session=FacebookSession(access_token=os.environ['FAB_META_ADS_TOKEN'])
if mode=='direct':
    session.GRAPH=os.environ['FAB_META_ADS_URL'].rstrip('/')
else:
    session.requests.verify=os.environ['SSL_CERT_FILE']
api=FacebookAdsApi(session,api_version='v23.0')
identifier=os.environ.get('FAB_META_ADSET_ID','130001')
account=AdAccount(os.environ.get('FAB_META_ACCOUNT_ID','act_10001'),api=api)
sets=list(account.get_ad_sets(fields=['id','name','daily_budget'],params={'limit':1}))
assert len(sets)>=3 and len({row['id'] for row in sets})==len(sets)
node=AdSet(identifier,api=api)
before=node.api_get(fields=['id','name','daily_budget'])
old=int(before['daily_budget'])
target=old+100
node.api_update(params={'daily_budget':target})
after=AdSet(identifier,api=api).api_get(fields=['id','daily_budget'])
assert int(after['daily_budget'])==target
print(json.dumps({'mode':mode,'client':'facebook-business 26.0.2','before':old,'after':target,'readback':'passed','paginated_adsets':len(sets)}))
