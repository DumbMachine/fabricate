from pathlib import Path
import json,datetime,random,argparse
parser=argparse.ArgumentParser(description="Rebuild the static large publisher baselines, without starting any services")
parser.add_argument("--check",action="store_true",help="fail on drift instead of writing")
args=parser.parse_args()
root=Path(__file__).resolve().parents[2]
rng=random.Random(481516)
start=datetime.date(2026,5,1)
dates=[start+datetime.timedelta(days=i) for i in range(180)]
def load(r):return json.loads((root/f'resources/{r}/scenarios/food-publisher.v1.json').read_text())
def save(r,d):
 d['$id']=r+'.food-publisher-scale.v1'
 # One record per line keeps large static datasets inspectable without bloating them.
 envelope={k:v for k,v in d.items() if k!='state'}
 lines=[json.dumps(envelope)[:-1]+',"state":{']
 for n,(k,v) in enumerate(d['state'].items()):
  tail=',' if n<len(d['state'])-1 else ''
  if isinstance(v,list):lines.extend(['  '+json.dumps(k)+':[',',\n'.join('    '+json.dumps(x,separators=(',',':')) for x in v),'  ]'+tail])
  else:lines.append('  '+json.dumps(k)+':'+json.dumps(v)+tail)
 lines.append('}}');p=root/f'resources/{r}/scenarios/food-publisher-scale.v1.json';content='\n'.join(lines)+'\n'
 if args.check:
  if p.read_text()!=content:raise SystemExit('dataset drift: '+str(p))
 else:p.write_text(content)
 print(r,{k:len(v) for k,v in d['state'].items() if isinstance(v,list)},p.stat().st_size)
meta=load('meta-ads');m=meta['state'];topics=['Weeknight dinners','Budget lunches','Freezer meals','Garden salads','Summer grilling','Baking basics','Holiday sides','Pantry staples','Family breakfasts','Slow cooker','Vegetarian mains','Kitchen tools']
audiences=['Broad audience','Returning readers','Newsletter subscribers']
for ai,(account,name,currency,campaigns) in enumerate([('act_10001','Hearth & Harvest','USD',45),('act_20002','Hearth & Harvest · UK','GBP',12),('act_30003','Pantry Press · sister publication','USD',12)]):
 if ai:m['accounts'].append({'id':account,'name':name,'currency':currency})
 for ci in range(campaigns):
  campaign=str(210000+ai*1000+ci);status=['ACTIVE','ACTIVE','PAUSED','ARCHIVED'][ci%4]
  m['campaigns'].append({'id':campaign,'account_id':account,'name':f'{topics[ci%len(topics)]} · {2026} {"evergreen" if ci%3 else "seasonal"} {ci//12+1}','status':status})
  for si in range(3):
   adset=str(310000+ai*10000+ci*3+si);state='PAUSED' if si==2 and ci%7==0 else status
   m['adsets'].append({'id':adset,'account_id':account,'campaign_id':campaign,'name':f'{topics[ci%len(topics)]} · {audiences[si]} · {ci//12+1}','status':state,'daily_budget':str([2500,4000,6000,9000,12000][(ci+si)%5])})
   launched=(ci*7+si*3)%90;ended=105+ci%30 if status=='ARCHIVED' else 165+ci%15 if state=='PAUSED' else 180
   for di,day in enumerate(dates):
    if di<launched or di>=ended or (di+ci+si)%29==0:continue
    # Seasonality, weekday effects and varied delivery; repeated high frequency is not a unique answer key.
    impressions=1200+rng.randrange(15000);ctr=[.006,.012,.018,.027][(ci+si)%4]
    clicks=round(impressions*ctr);spend=900+rng.randrange(8500)
    frequency=1.1+(ci%4)*.35+si*.12+max(0,di-145)*(.04 if ci%9==0 else .005)
    if ci%11==0:frequency+=1.8
    m['insights'].append({'id':f'{adset}-{day}','account_id':account,'campaign_id':campaign,'adset_id':adset,'date_start':str(day),'date_stop':str(day),'impressions':str(impressions),'clicks':str(clicks),'spend':f'{spend/100:.2f}','frequency':f'{frequency:.2f}'})
# Extend original campaigns across history without replacing their known October observations.
for si,adset in enumerate(m['adsets'][:3]):
 for di,day in enumerate(dates):
  if '2026-10-01'<=str(day)<='2026-10-14':continue
  m['insights'].append({'id':f'{adset["id"]}-{day}','account_id':adset['account_id'],'campaign_id':adset['campaign_id'],'adset_id':adset['id'],'date_start':str(day),'date_stop':str(day),'impressions':str(4000+di*17),'clicks':str(80+di%30),'spend':f'{(3600+si*1100+di%700)/100:.2f}','frequency':f'{1.2+max(0,di-140)*.055+si*.2:.2f}'})
save('meta-ads',meta)
ga=load('ga4');g=ga['state']
for pid,name,currency in [('410000002','Hearth & Harvest UK','GBP'),('410000003','Pantry Press','USD')]:g['properties'].append({'propertyId':pid,'displayName':name,'currencyCode':currency,'timeZone':'Europe/London' if currency=='GBP' else 'America/Chicago'})
channels=[('google','organic','Evergreen recipes'),('direct','none','Direct visits'),('newsletter','email','Weekly meal plan'),('pinterest','organic_social','Recipe inspiration'),('instagram','paid_social','Weeknight recipes'),('facebook','paid_social','Seasonal recipe discovery'),('referral','referral','Partner recipe roundup'),('bing','organic','Recipe search')]
for pi,prop in enumerate(g['properties']):
 for di,day in enumerate(dates):
  for ci,(source,medium,campaign) in enumerate(channels):
   for ei,event in enumerate(['page_view','session_start','scroll','sign_up','purchase']):
    count=(150+di%70+ci*29+rng.randrange(100)) if ei<3 else (2+(di+ci)%8 if ei==3 else (di+ci)%4)
    g['events'].append({'propertyId':prop['propertyId'],'date':str(day),'eventName':event,'transactionId':f'{pi}-{day}-{ci}-{ei}','sessionSource':source,'sessionMedium':medium,'sessionCampaignName':campaign,'eventCount':count,'transactions':count if event=='purchase' else 0,'purchaseRevenue':count*[19,24,29][(ci+pi)%3] if event=='purchase' else 0,'itemRefundAmount':0})
   if (di+ci)%17==0:g['events'].append({'propertyId':prop['propertyId'],'date':str(day),'eventName':'refund','transactionId':f'refund-{pi}-{day}-{ci}','sessionSource':source,'sessionMedium':medium,'sessionCampaignName':campaign,'eventCount':1,'transactions':0,'purchaseRevenue':0,'itemRefundAmount':24})
save('ga4',ga)
impact=load('impact');a=impact['state'];names=['Kitchen essentials','Pantry subscriptions','Cookbook marketplace','Bakeware collective','Garden to table','Meal planning','Coffee corner','Weekend hosting']
for i,name in enumerate(names):
 program=4101+i
 if i:a['programs'].append({'id':program,'name':name+' program','state':'CLOSED' if i==7 else 'ACTIVE','type':'Performance','shortDescription':'Synthetic publisher commerce program; independent of advertising attribution.'})
 for j in range(8):
  partner=f'partner-{program}-{j}'
  a['partners'].append({'id':partner,'campaignId':program,'name':f'{name} partner {j+1}','state':'SUSPENDED' if j==7 else 'ACTIVE','currency':'USD','website':f'https://partner-{program}-{j}.example','description':'Fictional retail and editorial partner','dateCreated':'2026-04-01T09:00:00Z'})
  for k in range(450 if i==0 else 75):
   di=(k*13+j*7+i*19)%180;day=dates[di];stamp=str(day)+f'T{8+k%12:02}:15:00Z';oid=f'commerce-{program}-{j}-{k}'
   amount=1900+rng.randrange(38000);payout=round(amount*[.04,.075,.10][k%3]);state=['APPROVED']*7+['PENDING']*2+['REVERSED']
   a['actions'].append({'id':f'action-{program}-{j}-{k:04}','campaignId':program,'partnerId':partner,'oid':oid,'state':state[(k+j)%10],'payoutPaise':payout,'amountPaise':amount,'currency':'USD','eventDate':stamp,'creationDate':stamp,'customerCity':'','customerRegion':'','customerCountry':['US','CA','GB'][k%3],'customerPostCode':'','note':'Returned order' if (k+j)%10==9 else '', 'items':[{'sku':f'SKU-{i}-{k%30}','name':topics[k%12],'quantity':1,'saleAmountPaise':amount}]})
  for k,content in enumerate(['Monthly reconciliation complete.','Review pending conversions after the return window.','Seasonal placement schedule confirmed.']):
   a['notes'].append({'id':f'note-{program}-{j}-{k}','campaignId':program,'partnerId':partner,'creator':'Partner operations','content':content,'type':'EMAIL','creationDate':f'2026-0{5+k}-15T09:00:00Z','modificationDate':f'2026-0{5+k}-15T09:00:00Z'})
for kind in ['programs','partners','actions','notes']:a[kind].sort(key=lambda row:row['id'])
save('impact',impact)
