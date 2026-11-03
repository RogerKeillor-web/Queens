// Shared helpers for the Queens table and player screens.
(function () {
  'use strict';

  const SUIT = { S: '♠', H: '♥', D: '♦', C: '♣' };
  // Display / sort order: 3 up to Ace, then the wild 2s, then jokers.
  const RANK_ORDER = ['3', '4', '5', '6', '7', '8', '9', '10', 'J', 'Q', 'K', 'A', '2', 'JOKER'];
  const SUIT_ORDER = ['S', 'H', 'C', 'D', 'R', 'B'];
  const SEAT_NAMES = ['North', 'East', 'South', 'West'];

  const isRed = (c) => c.suit === 'H' || c.suit === 'D' || c.suit === 'R';
  const isWild = (c, jokersWild) => c.rank === '2' || (!!jokersWild && c.rank === 'JOKER');

  function sortCards(cards) {
    return cards.slice().sort((a, b) =>
      (RANK_ORDER.indexOf(a.rank) - RANK_ORDER.indexOf(b.rank)) ||
      (SUIT_ORDER.indexOf(a.suit) - SUIT_ORDER.indexOf(b.suit)) ||
      (a.id - b.id));
  }

  // Everything user-supplied is set with textContent; card data comes from the server.
  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function cardEl(c, opts) {
    opts = opts || {};
    const e = el('div', 'card ' + (isRed(c) ? 'red' : 'black'));
    if (c.rank === 'JOKER') e.classList.add('joker');
    if (opts.wild) e.classList.add('wild');
    e.dataset.id = c.id;
    const corner = el('span', 'corner');
    if (c.rank === 'JOKER') {
      corner.appendChild(el('b', null, '★'));
      e.append(corner, el('span', 'pip', 'JOKER'));
    } else {
      corner.append(el('b', null, c.rank), el('i', null, SUIT[c.suit]));
      e.append(corner, el('span', 'pip', SUIT[c.suit]));
    }
    return e;
  }

  function backEl() { return el('div', 'card back'); }

  // A meld: rank label, overlapped cards (wilds last, marked), point value.
  function meldEl(m, jokersWild, extraClass) {
    const e = el('div', 'meld' + (extraClass ? ' ' + extraClass : ''));
    e.dataset.meld = m.id;
    e.appendChild(el('div', 'mlabel', m.rank === 'JOKER' ? '★' : m.rank));
    const cs = el('div', 'mcards');
    m.cards.forEach((c) => cs.appendChild(cardEl(c, { wild: isWild(c, jokersWild) })));
    e.appendChild(cs);
    e.appendChild(el('div', 'mpts', m.points + ' pts'));
    return e;
  }

  function seatName(seats, seat) {
    const s = seats && seats[seat];
    return s && s.name ? s.name : SEAT_NAMES[seat];
  }

  // Team 0 = North + South, team 1 = East + West.
  function teamName(seats, team) {
    return seatName(seats, team) + ' & ' + seatName(seats, team + 2);
  }

  const signed = (n) => (n > 0 ? '+' + n : n < 0 ? '−' + Math.abs(n) : '0');

  // WebSocket with automatic reconnect. Messages are JSON objects.
  function connect(handlers) {
    let ws = null, retry = 0;
    const api = {
      send(obj) {
        if (ws && ws.readyState === 1) { ws.send(JSON.stringify(obj)); return true; }
        return false;
      },
      get open() { return !!ws && ws.readyState === 1; },
    };
    function open() {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      ws = new WebSocket(proto + '://' + location.host + '/ws');
      ws.onopen = () => { retry = 0; handlers.onOpen && handlers.onOpen(); };
      ws.onmessage = (e) => {
        let m;
        try { m = JSON.parse(e.data); } catch (_) { return; }
        handlers.onMsg && handlers.onMsg(m);
      };
      ws.onclose = () => {
        handlers.onClose && handlers.onClose();
        retry += 1;
        setTimeout(open, Math.min(4000, 400 * retry));
      };
      ws.onerror = () => { try { ws.close(); } catch (_) { /* ignore */ } };
    }
    open();
    return api;
  }

  let toastTimer = null;
  function toast(msg, kind) {
    let t = document.getElementById('toast');
    if (!t) { t = el('div', 'toast'); t.id = 'toast'; t.setAttribute('role', 'status'); document.body.appendChild(t); }
    t.textContent = msg;
    t.className = 'toast show' + (kind === 'info' ? ' info' : '');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.classList.remove('show'); }, kind === 'info' ? 2200 : 3600);
  }

  // Build the hand-result table shared by both screens.
  function resultTable(result, seats) {
    const t = el('table', 'result');
    const head = el('tr');
    ['Team', 'Melds', 'Hand', 'This hand', 'Score'].forEach((h) => head.appendChild(el('th', null, h)));
    t.appendChild(head);
    const order = [result.firstCount, 1 - result.firstCount];
    order.forEach((team, i) => {
      const r = result.teams[team];
      const tr = el('tr', 't' + team);
      const first = el('td');
      first.appendChild(el('div', null, teamName(seats, team)));
      const tags = [];
      if (i === 0) tags.push('counted first');
      if (result.winner === team) tags.push('went out');
      if (tags.length) first.appendChild(el('div', null, tags.join(' · '))).style.cssText = 'font-size:.7rem;font-weight:500;color:var(--muted)';
      tr.appendChild(first);
      tr.appendChild(el('td', null, '+' + r.meldPts));
      tr.appendChild(el('td', r.handPts ? 'neg' : null, r.handPts ? '−' + r.handPts : '–'));
      tr.appendChild(el('td', (r.delta >= 0 ? 'pos' : 'neg') + ' big', signed(r.delta)));
      tr.appendChild(el('td', 'big', String(r.total)));
      t.appendChild(tr);
    });
    return t;
  }

  window.Q = {
    SUIT, RANK_ORDER, SEAT_NAMES, isRed, isWild, sortCards, el, cardEl, backEl, meldEl,
    seatName, teamName, signed, connect, toast, resultTable,
  };
})();
