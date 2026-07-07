// Neobank ledger seed. Runs under mongosh as root from
// /docker-entrypoint-initdb.d/. Switch to the `bank` db first —
// the image's init step otherwise lands you in `test`.

db = db.getSiblingDB('bank');

const ACCOUNTS = fabEnvInt('NEOBANK_ACCOUNTS', 200);
const TXNS = fabEnvInt('NEOBANK_TXNS', 5000);
const STRUCTURING_DEPOSITS = fabEnvInt('NEOBANK_STRUCTURING_DEPOSITS', 12);
const SANCTIONS_OUTBOUND_TRANSFERS = fabEnvInt('NEOBANK_SANCTIONS_OUTBOUND_TRANSFERS', 4);
const STUCK_PENDING_TXNS = fabEnvInt('NEOBANK_STUCK_PENDING_TXNS', 5);
const DAY_MS = 24 * 60 * 60 * 1000;
const START = new Date('2024-06-01T00:00:00Z');

function rnd(n) { return Math.floor(Math.random() * n); }
function pick(arr) { return arr[rnd(arr.length)]; }
function jitter(base, ms) { return new Date(base.getTime() + rnd(ms)); }

const CURRENCIES = ['USD', 'USD', 'USD', 'EUR', 'GBP'];
const TYPES = ['deposit', 'deposit', 'withdrawal', 'transfer', 'card_payment', 'card_payment', 'fee'];
const STATUSES = ['posted', 'posted', 'posted', 'posted', 'pending', 'reversed'];
const CATEGORIES = [
  'groceries', 'restaurants', 'transport', 'utilities',
  'entertainment', 'travel', 'subscriptions', 'rent', 'salary',
];

// ── accounts ────────────────────────────────────────────────────
const accounts = [];
for (let i = 1; i <= ACCOUNTS; i++) {
  const opened = new Date(START.getTime() - rnd(365) * DAY_MS);
  accounts.push({
    _id: 'acct_' + String(i).padStart(6, '0'),
    holder_name: 'Account Holder ' + i,
    currency: pick(CURRENCIES),
    kyc_status: i <= 180 ? 'approved' : pick(['pending', 'rejected']),
    balance_cents: 0, // recomputed after txns
    opened_at: opened,
  });
}
db.accounts.insertMany(accounts);

// ── kyc events ──────────────────────────────────────────────────
const kyc = [];
for (const a of accounts) {
  kyc.push({ account_id: a._id, ts: a.opened_at, event: 'started' });
  kyc.push({ account_id: a._id, ts: new Date(a.opened_at.getTime() + 60 * 1000), event: 'submitted' });
  if (a.kyc_status === 'approved') {
    kyc.push({ account_id: a._id, ts: new Date(a.opened_at.getTime() + 5 * 60 * 1000), event: 'approved' });
  } else if (a.kyc_status === 'rejected') {
    kyc.push({ account_id: a._id, ts: new Date(a.opened_at.getTime() + 5 * 60 * 1000), event: 'rejected' });
  }
}

// Bug seed #2: sanctions_hit on acct_000042, which still has
// transactions after this event in the seeded data below.
kyc.push({
  account_id: 'acct_000042',
  ts: new Date('2024-07-15T10:00:00Z'),
  event: 'sanctions_hit',
  document_ref: 'OFAC-SDN-' + Date.now(),
});
db.kyc_events.insertMany(kyc);

// ── transactions ────────────────────────────────────────────────
const txns = [];
for (let i = 1; i <= TXNS; i++) {
  const acct = accounts[rnd(ACCOUNTS)];
  const type = pick(TYPES);
  const amount =
    type === 'fee' ? 50 + rnd(900) :
    type === 'card_payment' ? 100 + rnd(15000) :
    type === 'deposit' ? 5000 + rnd(500000) :
    1000 + rnd(100000);
  txns.push({
    _id: 'txn_' + String(i).padStart(8, '0'),
    account_id: acct._id,
    ts: jitter(START, 90 * DAY_MS),
    type,
    amount_cents: type === 'withdrawal' || type === 'card_payment' || type === 'fee' ? -amount : amount,
    currency: acct.currency,
    status: pick(STATUSES),
    counterparty: type === 'transfer' ? 'acct_' + String(1 + rnd(ACCOUNTS)).padStart(6, '0') : null,
    merchant_category: type === 'card_payment' ? pick(CATEGORIES) : null,
    idempotency_key: 'idem_' + i + '_' + rnd(999999),
  });
}

// Bug seed #1: structuring pattern on acct_000091. Twelve deposits
// between $9,400 and $9,900 within a 24h window.
const burstAt = new Date('2024-07-10T08:00:00Z');
const structuringIntervalMs = Math.max(60 * 1000, Math.floor(DAY_MS / Math.max(STRUCTURING_DEPOSITS, 1)));
for (let i = 0; i < STRUCTURING_DEPOSITS; i++) {
  txns.push({
    _id: 'txn_aml_' + i,
    account_id: 'acct_000091',
    ts: new Date(burstAt.getTime() + i * structuringIntervalMs),
    type: 'deposit',
    amount_cents: 940000 + rnd(60000),
    currency: 'USD',
    status: 'posted',
    counterparty: null,
    merchant_category: null,
    idempotency_key: 'idem_aml_' + i,
  });
}

// Bug seed #2 (continued): outbound transfers from acct_000042
// after the sanctions_hit event.
for (let i = 0; i < SANCTIONS_OUTBOUND_TRANSFERS; i++) {
  txns.push({
    _id: 'txn_san_' + i,
    account_id: 'acct_000042',
    ts: new Date('2024-07-15T12:00:00Z').getTime() + i * 6 * 60 * 60 * 1000,
    type: 'transfer',
    amount_cents: -250000 - i * 50000,
    currency: 'USD',
    status: 'posted',
    counterparty: 'acct_000170',
    merchant_category: null,
    idempotency_key: 'idem_san_' + i,
  });
}
// Coerce ts back to Date since we did .getTime() arithmetic.
for (const t of txns) { if (typeof t.ts === 'number') t.ts = new Date(t.ts); }

// Bug seed #3: stuck-pending older than 48h.
for (let i = 0; i < STUCK_PENDING_TXNS; i++) {
  txns.push({
    _id: 'txn_stuck_' + i,
    account_id: 'acct_' + String(10 + i).padStart(6, '0'),
    ts: new Date(START.getTime() + 10 * DAY_MS),
    type: 'card_payment',
    amount_cents: -(1500 + rnd(8000)),
    currency: 'USD',
    status: 'pending',
    counterparty: null,
    merchant_category: 'subscriptions',
    idempotency_key: 'idem_stuck_' + i,
  });
}

db.transactions.insertMany(txns);

// Indexes the back-of-house team would actually create.
db.transactions.createIndex({ account_id: 1, ts: -1 });
db.transactions.createIndex({ status: 1, ts: 1 });
db.transactions.createIndex({ idempotency_key: 1 }, { unique: true });
db.kyc_events.createIndex({ account_id: 1, ts: -1 });
db.kyc_events.createIndex({ event: 1 });

print('bank.accounts: '     + db.accounts.countDocuments({}));
print('bank.transactions: ' + db.transactions.countDocuments({}));
print('bank.kyc_events: '   + db.kyc_events.countDocuments({}));
