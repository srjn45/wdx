// The made-up project the demo shows, shop-api: its files, diffs, docs, agents
// and their screens, worktrees, plan and terminals. mkState returns a fresh copy.
// The agents' screens are mock-ups in each AI CLI's style, never copies of them.
import { P, seg } from './palette.js';

export const FILES = {
  'cmd/server/main.go': `package main

import (
\t"log"
\t"net/http"

\t"shop/internal/cart"
\t"shop/internal/checkout"
)

func main() {
\tmux := http.NewServeMux()
\tmux.Handle("/cart/", cart.Handler())
\tmux.Handle("/checkout", checkout.Handler())
\tlog.Println("listening on :8080")
\tlog.Fatal(http.ListenAndServe(":8080", mux))
}`,
  'internal/cart/cart.go': `package cart

// Item is one line of a cart.
type Item struct {
\tSKU   string
\tQty   int
\tPrice int // cents
}

type Cart struct{ Items []Item }

// Total is what the cart costs, in cents.
func (c *Cart) Total() int {
\tt := 0
\tfor _, it := range c.Items {
\t\tt += it.Qty * it.Price
\t}
\treturn t
}`,
  'internal/checkout/checkout.go': `package checkout

import (
\t"encoding/json"
\t"net/http"

\t"shop/internal/cart"
)

type Order struct {
\tID    string \`json:"id"\`
\tTotal int    \`json:"total"\`
}

func Handler() http.Handler {
\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
\t\tvar c cart.Cart
\t\tif err := json.NewDecoder(r.Body).Decode(&c); err != nil {
\t\t\thttp.Error(w, "bad cart", http.StatusBadRequest)
\t\t\treturn
\t\t}
\t\tjson.NewEncoder(w).Encode(Order{ID: newID(), Total: c.Total()})
\t})
}`,
  'internal/checkout/id.go': `package checkout

func newID() string { return "ord_" + randHex(8) }`,
  'internal/auth/session.go': `package auth

import "time"

type Session struct {
\tUser    string
\tExpires time.Time
}

func NewSession(cfg Config, user string) Session {
\treturn Session{
\t\tUser:    user,
\t\tExpires: time.Now().Add(time.Duration(cfg.TTL) * time.Minute),
\t}
}`,
  'web/src/App.tsx': `import { Cart } from "./Cart";

export function App() {
  return (
    <main>
      <h1>shop</h1>
      <Cart />
    </main>
  );
}`,
  'go.mod': `module shop

go 1.26`,
  'README.md': `# shop-api

The cart and checkout service.

    go run ./cmd/server`,
};

export const DIFFS = {
  'checkout-flow': `2 commits ahead of main · +20 −2 · 2 files
diff --git a/internal/checkout/checkout.go b/internal/checkout/checkout.go
--- a/internal/checkout/checkout.go
+++ b/internal/checkout/checkout.go
@@ -1,9 +1,24 @@ package checkout
 package checkout

-import "net/http"
+import (
+\t"encoding/json"
+\t"net/http"
+
+\t"shop/internal/cart"
+)
+
+type Order struct {
+\tID    string \`json:"id"\`
+\tTotal int    \`json:"total"\`
+}

 func Handler() http.Handler {
 \treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
-\t\tw.WriteHeader(http.StatusNotImplemented)
+\t\tvar c cart.Cart
+\t\tif err := json.NewDecoder(r.Body).Decode(&c); err != nil {
+\t\t\thttp.Error(w, "bad cart", http.StatusBadRequest)
+\t\t\treturn
+\t\t}
+\t\tjson.NewEncoder(w).Encode(Order{ID: newID(), Total: c.Total()})
 \t})
 }
diff --git a/internal/checkout/id.go b/internal/checkout/id.go
new file mode 100644
--- /dev/null
+++ b/internal/checkout/id.go
@@ -0,0 +1,3 @@
+package checkout
+
+func newID() string { return "ord_" + randHex(8) }`,
  'fix-auth': `1 commit ahead of main · +31 −1 · 2 files
diff --git a/internal/auth/session.go b/internal/auth/session.go
--- a/internal/auth/session.go
+++ b/internal/auth/session.go
@@ -11,6 +11,6 @@ func NewSession(cfg Config, user string) Session {
 \treturn Session{
 \t\tUser:    user,
-\t\tExpires: time.Now().Add(time.Duration(cfg.TTL) * time.Minute),
+\t\tExpires: time.Now().Add(time.Duration(cfg.TTL) * time.Hour),
 \t}
 }
diff --git a/internal/auth/session_test.go b/internal/auth/session_test.go
new file mode 100644
--- /dev/null
+++ b/internal/auth/session_test.go
@@ -0,0 +1,12 @@
+package auth
+
+func TestSessionLifetime(t *testing.T) {
+\tt.Parallel()
+\ts := NewSession(Config{TTL: 24}, "ada")
+\tgot := time.Until(s.Expires).Round(time.Hour)
+\tif got != 24*time.Hour {
+\t\tt.Fatalf("session lasts %v, want 24h", got)
+\t}
+}`,
  'main': `not committed · 1 file
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,5 @@
 # shop-api

 The cart and checkout service.
+
+    go run ./cmd/server`,
};

export const DOCS = {
  CHECKOUT: { status: 'approved', rev: 3, labels: 'design', body: `# Checkout

Status: approved

## Goal
A customer places an order from their cart, and gets a receipt by email.

## API
POST /checkout takes the cart and returns the order:
    {"id": "ord_3f9a1c2e", "total": 4200}

## Out of scope
Taxes and discounts: a later plan.` },
  SESSIONS: { status: 'review', rev: 2, labels: 'auth', body: `# Sessions

Status: review

Sessions last 24 hours, set by auth.ttl_hours.
A session is renewed on every request in its last hour.` },
  RELEASE_0_5: { status: 'wip', rev: 1, labels: '', body: `# Release 0.5

- checkout: place an order
- auth: sessions last 24 hours` },
};

export const ACA = {
  claude: { label: 'claude code', icon: '✻', iconFg: P.orange, bullet: '●', model: 'opus' },
  codex: { label: 'codex', icon: '>_', iconFg: P.fg, bullet: '•', model: 'gpt-5' },
  cursor: { label: 'cursor agent', icon: '⬡', iconFg: P.blue, bullet: '→', model: 'auto' },
  antigravity: { label: 'antigravity', icon: '▲', iconFg: P.cyan, bullet: '◆', model: 'gemini' },
};
export const ACA_NAMES = { claude: 'Claude Code', codex: 'Codex', cursor: 'Cursor', antigravity: 'Antigravity' };

export function header(a) {
  const k = ACA[a.aca];
  return [[seg(k.icon + ' ', k.iconFg, true), seg(k.label, P.white, true), seg('  ·  ' + a.model + '  ·  ~/code/shop-api' + (a.wt ? '/.worktrees/' + a.wt : ''), P.dim)], []];
}
export const user = (t) => [seg('> ', P.dim), seg(t, P.white, true)];
export const say = (a, t, fg) => [seg(ACA[a.aca].bullet + ' ', fg || P.fg), seg(t, P.fg)];
export const sub = (t, fg) => [seg('  ⎿ ', P.dim), seg(t, fg || P.dim)];
export const blank = () => [];

export function mkAgents() {
  const list = [
    { id: 'reviewer', name: 'reviewer', aca: 'claude', model: 'opus', wt: 'checkout-flow', state: 'working', ctx: '82k',
      script: (a) => ({ start: [user('Review the checkout-flow branch before it lands.'), blank(), say(a, 'Reading the branch\'s diff against main.'), sub('git diff main...checkout-flow · +20 −2 in 2 files'), blank()],
        stream: [say(a, 'Read internal/checkout/checkout.go (24 lines)'), say(a, 'Read internal/checkout/id.go (3 lines)'), blank(),
          say(a, 'Found 2 problems:'), [seg('  1. id.go calls randHex, which is defined nowhere: the build fails.')], [seg('  2. Encode\'s error is dropped: a client that hangs up leaves no log line.')], blank(),
          say(a, 'Writing randHex with crypto/rand, and a test for it.'), sub('Edit internal/checkout/id.go · +9 −1', P.green),
          say(a, 'Bash go test ./internal/checkout/...'), sub('ok  shop/internal/checkout  0.41s', P.green), blank(),
          say(a, 'Done: two fixes committed on checkout-flow. It is ready to land.', P.green)], done: 'idle' }) },
    { id: 'reviewer-tests', name: 'reviewer-tests', parent: 'reviewer', aca: 'codex', model: 'gpt-5', wt: 'checkout-flow', state: 'idle', ctx: '31k',
      script: (a) => ({ start: [user('Write table tests for cart.Total.'), blank(), say(a, 'Added TestTotal with 6 cases, including an empty cart.'), say(a, 'go test ./internal/cart/... · ok')], stream: [] }) },
    { id: 'fix-auth', name: 'fix-auth', aca: 'codex', model: 'gpt-5', wt: 'fix-auth', state: 'waiting', ctx: '154k',
      prompt: { q: 'Run this command?', cmd: '$ go test ./internal/auth/...', opts: ['Yes', 'Yes, and don\'t ask again for go test', 'No, and tell codex what to do'], sel: 0 },
      script: (a) => ({ start: [user('Sessions expire after 24 minutes instead of 24 hours. Find out why and fix it.'), blank(),
        say(a, 'NewSession multiplies cfg.TTL by time.Minute; the setting is in hours.'), say(a, 'Changed it to time.Hour and added TestSessionLifetime.'), blank()], stream: [] }) },
    { id: 'docs', name: 'docs', aca: 'cursor', model: 'auto', state: 'idle', ctx: '12k',
      script: (a) => ({ start: [user('Write docs/CHECKOUT.md from the approved CHECKOUT doc.'), blank(), say(a, 'Wrote docs/CHECKOUT.md (64 lines) and linked it from README.md.'), say(a, 'Nothing else to do.')], stream: [] }) },
    { id: 'checkout-api', name: 'checkout-api', plan: 'checkout', aca: 'claude', model: 'sonnet', wt: 'checkout-api', state: 'working', ctx: '64k', loop: true,
      script: (a) => ({ start: [user('Task api of plan checkout: POST /checkout places an order.'), blank()],
        stream: [say(a, 'Read internal/checkout/checkout.go'), say(a, 'Read migrations/0007_orders.sql'), say(a, 'Writing the handler on top of the orders table.'), sub('Edit internal/checkout/checkout.go · +38 −4', P.green), say(a, 'Bash go test ./internal/checkout/...'), sub('FAIL TestPlaceOrder: total 4100, want 4200', P.red), say(a, 'The fixture prices are in euros, not cents. Fixing the fixture.'), sub('Edit internal/checkout/testdata/cart.json · +3 −3', P.green), sub('ok  shop/internal/checkout  0.52s', P.green)] }) },
    { id: 'checkout-emails', name: 'checkout-emails', plan: 'checkout', aca: 'antigravity', model: 'gemini', wt: 'checkout-emails', state: 'working', ctx: '23k', loop: true,
      script: (a) => ({ start: [user('Task emails of plan checkout: send a receipt email.'), blank()],
        stream: [say(a, 'Looking for a mail package in the module…'), say(a, 'None: adding internal/mail with an SMTP sender.'), sub('Write internal/mail/mail.go · +54', P.green), say(a, 'Writing the receipt template.'), sub('Write internal/mail/receipt.tmpl · +18', P.green), say(a, 'go vet ./... · clean')] }) },
  ];
  for (const a of list) {
    const sc = a.script(a);
    a.lines = header(a).concat(sc.start);
    a.stream = sc.stream.slice(); a.streamAll = sc.stream.slice(); a.done = sc.done; a.input = ''; a.next = 0; a.started = 0;
    if (a.state === 'idle') { a.lines = a.lines.concat(sc.stream); a.stream = []; }
  }
  return list;
}

export function mkTree() {
  const f = (name, mark) => ({ name, mark });
  const d = (name, children, open, count) => ({ name, dir: true, children, open: !!open, count });
  return [
    d('.worktrees', [d('checkout-flow', []), d('fix-auth', []), d('checkout-api', []), d('checkout-emails', [])], false, 4),
    d('cmd', [d('server', [f('main.go')], true)], false),
    d('internal', [d('auth', [f('session.go')]), d('cart', [f('cart.go')]), d('checkout', [f('checkout.go'), f('id.go')])], false),
    d('web', [d('src', [f('App.tsx')])], false),
    f('go.mod'), f('README.md', 'M'),
  ];
}

export function mkState() {
  return {
    focus: 'left', lastTop: 'left', zoom: null, termOpen: true,
    popup: null, notice: null, noticeUntil: 0, quit: false, filter: null,
    collapsed: {}, cursor: 1, lscroll: 0, planFold: false,
    agents: mkAgents(),
    worktrees: [
      { name: 'main', ahead: 0, add: 0, del: 0, files: [{ st: 'M', path: 'README.md' }], open: false },
      { name: 'checkout-flow', ahead: 2, add: 20, del: 2, files: [{ st: 'M', path: 'internal/checkout/checkout.go' }, { st: 'A', path: 'internal/checkout/id.go' }], open: false },
      { name: 'fix-auth', ahead: 1, add: 31, del: 1, files: [{ st: 'M', path: 'internal/auth/session.go' }, { st: 'A', path: 'internal/auth/session_test.go' }], open: false },
    ],
    docFolders: [{ name: 'auth', open: true, docs: ['SESSIONS'] }], docs: ['CHECKOUT', 'RELEASE_0_5'],
    plans: [
      { name: 'checkout', state: 'in progress', progress: '1/4', tasks: [
        { id: 'schema', state: 'done', prompt: 'Add an orders table with id, cart and total.', after: [] },
        { id: 'api', state: 'working', agent: 'checkout-api', prompt: 'POST /checkout places an order from the cart.', after: ['schema'] },
        { id: 'emails', state: 'working', agent: 'checkout-emails', prompt: 'Send a receipt email once an order is placed.', after: ['schema'] },
        { id: 'docs', state: 'waiting', prompt: 'Document checkout in docs/CHECKOUT.md.', after: ['api', 'emails'] },
      ], sel: 1 },
      { name: 'search', state: 'draft', progress: '3 tasks', tasks: [], sel: 0 },
    ],
    planView: 0,
    codeAt: 0,
    tree: mkTree(),
    tabs: [], active: -1,
    terms: [
      { name: 'server', lines: [prompt().concat([seg('go run ./cmd/server')]), [seg('listening on :8080')]], input: '', running: 'server', state: 'running', scroll: 0 },
      { name: 'tests', lines: [], input: '', running: null, state: 'running', scroll: 0 },
    ],
    termActive: 0,
    newNames: ['swift-falcon', 'ample-deer', 'quiet-otter', 'brave-lynx', 'calm-heron'],
  };
}
export function prompt() { return [seg('~/code/shop-api ', P.cyan), seg('main ', P.mag), seg('❯ ', P.green)]; }
