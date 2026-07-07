// Seed for the events anomaly profile. Runs under mongosh as root
// via /docker-entrypoint-initdb.d/. The mongo image scopes init
// scripts to the `test` db by default — switch to analytics first
// so the agent finds the collection on the auth-source path.

db = db.getSiblingDB('analytics');

const KINDS = ['view', 'click', 'error'];
const USERS = Array.from({ length: 200 }, (_, i) => 'u' + (i + 1));
const PAGES = ['/home', '/pricing', '/docs', '/blog', '/signup', '/login', '/dashboard'];
const EVENTS_MIN_PER_MINUTE = fabEnvInt('EVENTS_MIN_PER_MINUTE', 3);
const EVENTS_RANDOM_PER_MINUTE = fabEnvInt('EVENTS_RANDOM_PER_MINUTE', 5);
const EVENTS_BURST_ERRORS = fabEnvInt('EVENTS_BURST_ERRORS', 50);

// Anchor the day at a stable timestamp so queries are reproducible.
const DAY_START = new Date('2024-06-01T00:00:00Z');

function pick(arr) { return arr[Math.floor(Math.random() * arr.length)]; }
function jitter(base, ms) { return new Date(base.getTime() + Math.floor(Math.random() * ms)); }

const batch = [];
for (let m = 0; m < 24 * 60; m++) {
  // 30-minute zero-traffic gap from 13:00 to 13:30 UTC — one of the
  // seeded anomalies. The agent should spot it on a 5-minute bucket.
  if (m >= 13 * 60 && m < 13 * 60 + 30) continue;
  const minuteStart = new Date(DAY_START.getTime() + m * 60 * 1000);
  const events = EVENTS_MIN_PER_MINUTE + Math.floor(Math.random() * EVENTS_RANDOM_PER_MINUTE);
  for (let i = 0; i < events; i++) {
    batch.push({
      ts: jitter(minuteStart, 60 * 1000),
      kind: Math.random() < 0.05 ? 'error' : pick(['view', 'view', 'click']),
      user_id: pick(USERS),
      page: pick(PAGES),
    });
  }
}

// Anomaly #2: error burst from a single user between 09:00–09:05 UTC.
const burstStart = new Date(DAY_START.getTime() + 9 * 60 * 60 * 1000);
for (let i = 0; i < EVENTS_BURST_ERRORS; i++) {
  batch.push({
    ts: jitter(burstStart, 5 * 60 * 1000),
    kind: 'error',
    user_id: 'u42',
    page: '/dashboard',
    error: 'TimeoutError',
  });
}

db.events.insertMany(batch);
db.events.createIndex({ ts: 1 });
db.events.createIndex({ user_id: 1, ts: 1 });

print('analytics.events seeded: ' + db.events.countDocuments({}));
