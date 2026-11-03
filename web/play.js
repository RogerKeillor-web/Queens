// Queens - player screen (phone / tablet / desktop). Talks to the Go server over a
// WebSocket. The server owns every rule; this file only shows state and sends moves.
(function () {
  'use strict';
  const { el, cardEl, backEl, meldEl, teamName, seatName, SEAT_NAMES, RANK_ORDER } = Q;
  const $ = (id) => document.getElementById(id);

  const store = {
    get(k) { try { return localStorage.getItem('queens.' + k); } catch (_) { return null; } },
    set(k, v) { try { localStorage.setItem('queens.' + k, v); } catch (_) { /* private mode */ } },
    del(k) { try { localStorage.removeItem('queens.' + k); } catch (_) { /* ignore */ } },
  };

  let S = null;                       // last state message
  let you = -1;                       // my seat, -1 when not seated
  const selected = new Set();         // selected card ids
  let order = [];                     // my own card order (used when auto-sort is off)
  let autosort = store.get('autosort') !== '0';
  let drag = null;                    // active pointer interaction on a card
  let deferred = false;               // a render was skipped while dragging
  let confirming = false;             // the "take the pile?" panel is open
  let known = new Set();              // card ids seen in my hand last time
  let fresh = new Set();              // newly drawn cards (briefly highlighted)
  let wasMyTurn = false;

  const jw = () => !!(S && S.game.rules.jokersWild);
  const myTeam = () => (you >= 0 ? you % 2 : -1);
  const myTurn = () => !!S && you >= 0 && S.game.phase === 'playing' && S.game.turn === you;

  // ---------------------------------------------------------------- network

  const net = Q.connect({
    onOpen() {
      const tok = store.get('token');
      if (tok) net.send({ type: 'rejoin', token: tok });
    },
    onClose() { Q.toast('Connection lost – reconnecting…', 'info'); },
    onMsg(m) {
      switch (m.type) {
        case 'state': S = m; you = m.you; render(); break;
        case 'joined': store.set('token', m.token); if (m.name) store.set('name', m.name); break;
        case 'error':
          if (m.code === 'badtoken') store.del('token'); else Q.toast(m.msg);
          break;
        default: break;
      }
    },
  });

  const act = (type, extra) => net.send(Object.assign({ type }, extra || {}));

  // ---------------------------------------------------------------- helpers

  function points(c) {
    if (c.rank === 'JOKER') return 50;
    if (c.rank === '2') return 25;
    if (c.rank === 'Q' && c.suit === 'S') return 100;
    if (['10', 'J', 'Q', 'K', 'A'].includes(c.rank)) return 10;
    return 5;
  }

  function handCards() { return (S && S.game.hand) || []; }

  // The hand in the order it is shown: sorted by rank, or the player's own order.
  function displayCards() {
    const hand = handCards();
    if (autosort) return Q.sortCards(hand);
    const byId = new Map(hand.map((c) => [c.id, c]));
    const out = [], seen = new Set();
    order.forEach((id) => { if (byId.has(id) && !seen.has(id)) { out.push(byId.get(id)); seen.add(id); } });
    hand.forEach((c) => { if (!seen.has(c.id)) out.push(c); });
    order = out.map((c) => c.id);
    return out;
  }

  function saveOrder() { act('order', { cards: order.slice() }); }

  function button(label, cls, onClick, disabled) {
    const b = el('button', 'btn ' + (cls || ''), label);
    b.type = 'button';
    if (disabled) b.disabled = true;
    b.onclick = onClick;
    return b;
  }

  // ---------------------------------------------------------------- render

  function render() {
    if (drag && drag.started) { deferred = true; return; }
    deferred = false;
    const seated = you >= 0;
    $('join').classList.toggle('hidden', seated);
    $('game').classList.toggle('hidden', !seated);
    if (!seated) { renderJoin(); closeOverlay(); return; }

    // drop selections that left the hand; flag newly drawn cards
    const ids = new Set(handCards().map((c) => c.id));
    for (const id of [...selected]) if (!ids.has(id)) selected.delete(id);
    if (known.size && S.game.phase === 'playing') {
      ids.forEach((id) => { if (!known.has(id)) fresh.add(id); });
      if (fresh.size) setTimeout(() => { fresh.clear(); document.querySelectorAll('.card.fresh').forEach((n) => n.classList.remove('fresh')); }, 2200);
    }
    known = ids;

    const mine = myTurn();
    if (mine && !wasMyTurn && navigator.vibrate) { try { navigator.vibrate(80); } catch (_) { /* ignore */ } }
    wasMyTurn = mine;

    renderHud();
    renderOpponents();
    renderPiles();
    renderMelds();
    renderHand();
    renderActions();
    renderOverlay();
  }

  // ---- seat picker

  let joinBuilt = false;
  function renderJoin() {
    const nameBox = $('name');
    if (!joinBuilt) { nameBox.value = store.get('name') || ''; joinBuilt = true; }
    const pick = $('seatpick');
    pick.textContent = '';
    const g = S ? S.game : null;
    S && S.seats.forEach((s, i) => {
      const partner = S.seats[(i + 2) % 4];
      const b = el('button', 't' + (i % 2));
      b.type = 'button';
      b.appendChild(el('small', null, 'Team ' + (i % 2 === 0 ? 'North/South' : 'East/West')));
      b.appendChild(el('b', null, SEAT_NAMES[i]));
      b.appendChild(el('small', null, s.taken ? (s.bot ? '🤖 ' : '') + s.name : 'Free · partner ' + (partner.taken ? partner.name : 'TBD')));
      if (s.taken) b.disabled = true;
      b.onclick = () => {
        const name = nameBox.value.trim();
        store.set('name', name);
        act('join', { seat: i, name });
      };
      pick.appendChild(b);
    });
    const free = S ? S.seats.filter((s) => !s.taken).length : 4;
    $('joinhint').textContent = !S ? 'Connecting…'
      : free === 0 ? 'The table is full. If this is your device, it should reconnect by itself – otherwise ask the host to reset the table.'
      : (g.phase !== 'idle' ? 'A game is in progress – take a free seat to join it.' : '');
  }

  // ---- HUD

  function statusText() {
    const g = S.game;
    if (g.phase === 'idle') {
      const n = S.seats.filter((s) => s.taken).length;
      return { main: 'Waiting for players (' + n + '/4)', sub: 'Partners sit across from each other' };
    }
    if (g.phase === 'handover') return { main: 'Hand over', sub: '' };
    if (g.phase === 'gameover') return { main: 'Game over', sub: '' };
    if (myTurn()) {
      return g.turnStep === 'draw'
        ? { main: 'Your turn', sub: 'Draw from the stock or take the pile', mine: true }
        : { main: 'Your turn', sub: 'Meld if you can, then discard', mine: true };
    }
    const who = seatName(S.seats, g.turn);
    return { main: who + (g.turnStep === 'draw' ? ' is drawing' : ' is playing'), sub: who === seatName(S.seats, (you + 2) % 4) ? 'Your partner' : '' };
  }

  function renderHud() {
    const g = S.game;
    for (let t = 0; t < 2; t++) {
      const box = $('sc' + t);
      box.textContent = '';
      box.classList.toggle('me', t === myTeam());
      box.appendChild(el('small', null, t === myTeam() ? 'Us' : 'Them'));
      box.appendChild(el('b', null, String(g.teams[t].score)));
    }
    const st = statusText(), box = $('status');
    box.textContent = '';
    box.classList.toggle('mine', !!st.mine);
    box.appendChild(document.createTextNode(st.main));
    if (st.sub) box.appendChild(el('small', null, st.sub));
  }

  function renderOpponents() {
    const g = S.game, box = $('opps');
    box.textContent = '';
    for (let k = 1; k <= 3; k++) {
      const i = (you + k) % 4, info = S.seats[i];
      const o = el('div', 'opp t' + (i % 2));
      if (k === 2) o.classList.add('partner');
      if (g.phase === 'playing' && g.turn === i) o.classList.add('turn');
      if (info.taken && !info.connected) o.classList.add('offline');
      o.appendChild(el('div', 'on', info.taken ? info.name : SEAT_NAMES[i]));
      let line = g.phase === 'idle' ? (info.taken ? 'ready' : 'empty') : g.handCounts[i] + ' cards';
      if (g.phase !== 'idle' && g.dealer === i) line += ' · dealer';
      o.appendChild(el('div', 'oc', line));
      box.appendChild(o);
    }
  }

  // ---- piles

  function pileEl(label, count, cards, active, onClick) {
    const wrap = el('div', 'pilewrap');
    const p = el('div', 'pile');
    const inner = el('div', 'cards-wrap');
    if (!cards.length) inner.appendChild(el('div', 'empty'));
    cards.forEach((c, i) => {
      const e = c ? cardEl(c, { wild: Q.isWild(c, jw()) }) : backEl();
      e.classList.add('stackcard');
      e.style.left = (i * 3) + 'px';
      e.style.top = (-i * 2) + 'px';
      inner.appendChild(e);
    });
    p.appendChild(inner);
    if (count > 0) p.appendChild(el('div', 'count', String(count)));
    if (active) p.classList.add('active');
    p.setAttribute('role', 'button');
    p.setAttribute('aria-label', label);
    p.tabIndex = 0;
    p.onclick = onClick;
    p.onkeydown = (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onClick(); } };
    wrap.appendChild(p);
    wrap.appendChild(el('div', 'pilelabel', label));
    return wrap;
  }

  function topName(c) { return c.rank === 'JOKER' ? 'Joker' : c.rank + Q.SUIT[c.suit]; }

  function renderPiles() {
    const g = S.game, box = $('piles'), hint = $('pilehint');
    box.textContent = '';
    hint.textContent = '';
    hint.classList.remove('warn');
    if (g.phase === 'idle') return;
    const backs = [];
    for (let i = 0; i < Math.min(3, Math.ceil(g.stockCount / 20)); i++) backs.push(null);
    box.appendChild(pileEl('Stock', g.stockCount, backs, g.canDraw, onStock));
    box.appendChild(pileEl('Discard', g.discardCount, (g.discardRecent || []).slice(-3), g.canPickup, onDiscard));
    if (g.discardTop) {
      if (myTurn() && g.turnStep === 'draw' && !g.canPickup && g.pickupBlocked) {
        hint.textContent = 'Top card ' + topName(g.discardTop) + ' – can\'t take the pile: ' + g.pickupBlocked;
        hint.classList.add('warn');
      } else if (myTurn() && g.turnStep === 'draw' && g.canPickup) {
        hint.textContent = 'You can take the pile (top card ' + topName(g.discardTop) + ')';
      } else {
        hint.textContent = 'Top of discard: ' + topName(g.discardTop);
      }
    }
  }

  function onStock() {
    if (!myTurn()) { Q.toast(S.game.phase === 'playing' ? 'Not your turn' : 'No hand in progress', 'info'); return; }
    if (S.game.turnStep !== 'draw') { Q.toast('You have already drawn – meld, then discard', 'info'); return; }
    act('draw');
  }

  function onDiscard() {
    const g = S.game;
    if (!myTurn()) { Q.toast(g.phase === 'playing' ? 'Not your turn' : 'No hand in progress', 'info'); return; }
    if (g.turnStep !== 'draw') { Q.toast('You have already drawn this turn', 'info'); return; }
    if (!g.canPickup) { Q.toast(g.pickupBlocked || 'You can\'t take the pile right now'); return; }
    confirmPickup();
  }

  function confirmPickup() {
    const g = S.game, top = g.discardTop, n = g.discardCount;
    const pickedIds = [...selected];
    confirming = true;
    const panel = $('panel');
    panel.textContent = '';
    panel.appendChild(el('h2', null, 'Take the pile?'));
    panel.appendChild(el('div', 'sub', n + ' cards. The top card, ' + topName(top) + ', must be melded straight away.'));
    panel.appendChild(el('div', null, pickedIds.length
      ? 'Using the ' + pickedIds.length + ' card(s) you selected.'
      : 'The best two matching cards from your hand are used automatically (two natural ' + top.rank + 's, or a ' + top.rank + ' and a wild card). Select your own cards first if you prefer.'));
    const row = el('div', 'row');
    row.appendChild(button('Not now', 'ghost', closeOverlay));
    row.appendChild(button('Take the pile', '', () => { closeOverlay(); act('pickup', pickedIds.length ? { cards: pickedIds } : {}); }));
    panel.appendChild(row);
    $('overlay').classList.remove('hidden');
  }

  function closeOverlay() {
    confirming = false;
    $('overlay').classList.add('hidden');
  }

  // ---- melds

  function renderMelds() {
    const g = S.game;
    const groups = [['mymelds', myTeam(), 'Our melds', true], ['theirmelds', 1 - myTeam(), 'Their melds', false]];
    const canAdd = myTurn() && g.turnStep === 'play' && selected.size > 0;
    groups.forEach(([id, team, title, mine]) => {
      const box = $(id), tv = g.teams[team];
      box.textContent = '';
      box.classList.add('t' + team);
      box.classList.remove('t' + (1 - team));
      const h = el('h3');
      h.append(el('span', null, title), el('span', null, g.phase === 'idle' ? '' : tv.meldPoints + ' pts'));
      box.appendChild(h);
      const wrap = el('div', 'wrap');
      if (!tv.melds.length) {
        wrap.appendChild(el('div', 'none', g.phase === 'idle' ? '' : (mine ? 'Nothing down yet – your team needs a meld to take the pile.' : 'Nothing down yet')));
      }
      tv.melds.slice().sort((a, b) => RANK_ORDER.indexOf(a.rank) - RANK_ORDER.indexOf(b.rank)).forEach((m) => {
        const e = meldEl(m, jw());
        if (mine && canAdd) {
          e.classList.add('target');
          e.onclick = () => act('add', { meld: m.id, cards: [...selected] });
        }
        wrap.appendChild(e);
      });
      box.appendChild(wrap);
    });
    if (canAdd) {
      const t = $('mymelds').querySelector('h3 span');
      if (t) t.textContent = 'Our melds – tap one to add your selection';
    }
  }

  // ---- hand

  function renderHand() {
    const box = $('hand'), g = S.game;
    box.textContent = '';
    box.classList.toggle('manual', !autosort);
    $('autosort').checked = autosort;
    const cards = displayCards();
    if (!cards.length) {
      if (g.phase !== 'idle') box.appendChild(el('div', 'empty', 'No cards in hand'));
      renderSelInfo(cards);
      return;
    }
    const W = box.clientWidth || (window.innerWidth - 16);
    const cw = Math.max(40, Math.min(64, Math.floor(W / 6.4)));
    box.style.setProperty('--cw', cw + 'px');
    const perRowMax = Math.max(1, Math.floor((W - cw) / (cw * 0.42)) + 1);
    const rows = Math.ceil(cards.length / perRowMax);
    const per = Math.ceil(cards.length / rows);
    for (let r = 0; r < rows; r++) {
      const chunk = cards.slice(r * per, (r + 1) * per);
      const row = el('div', 'row');
      row.style.justifyContent = 'center';
      if (r > 0) row.style.marginTop = (-cw * 1.4 * 0.46) + 'px';   // rows overlap; rank corners stay visible
      const step = chunk.length > 1 ? Math.min(cw * 0.64, (W - cw) / (chunk.length - 1)) : 0;
      chunk.forEach((c, i) => {
        const e = cardEl(c, { wild: Q.isWild(c, jw()) });
        if (i > 0) e.style.marginLeft = (step - cw) + 'px';
        if (selected.has(c.id)) e.classList.add('sel');
        if (fresh.has(c.id)) e.classList.add('fresh');
        if (c.rank === 'Q' && c.suit === 'S') e.classList.add('qs');
        e.setAttribute('role', 'listitem');
        row.appendChild(e);
      });
      box.appendChild(row);
    }
    renderSelInfo(cards);
  }

  function renderSelInfo(cards) {
    const info = $('selinfo');
    const sel = cards.filter((c) => selected.has(c.id));
    if (sel.length) info.textContent = sel.length + ' selected · ' + sel.reduce((n, c) => n + points(c), 0) + ' pts';
    else info.textContent = cards.length ? cards.length + ' cards · ' + cards.reduce((n, c) => n + points(c), 0) + ' pts' : '';
    $('clear').style.visibility = selected.size ? 'visible' : 'hidden';
  }

  // ---- action bar

  function doMeld() {
    const cards = handCards().filter((c) => selected.has(c.id));
    const naturals = cards.filter((c) => !Q.isWild(c, jw()));
    if (!naturals.length) { Q.toast('Wild cards can only be added to a meld – tap one of your melds above'); return; }
    const existing = S.game.teams[myTeam()].melds.find((m) => m.rank === naturals[0].rank);
    if (existing) act('add', { meld: existing.id, cards: [...selected] });
    else act('meld', { cards: [...selected] });
  }

  function doDiscard() {
    if (selected.size !== 1) { Q.toast('Select one card to discard', 'info'); return; }
    act('discard', { card: [...selected][0] });
  }

  function renderActions() {
    const g = S.game, box = $('actions');
    box.textContent = '';
    if (g.phase === 'idle') {
      const full = S.seats.every((s) => s.taken);
      if (!full) box.appendChild(button('Add bots', 'ghost', () => act('fillbots')));
      box.appendChild(button('Deal first hand', 'primary', () => act('start'), !full));
      box.appendChild(button('Leave seat', 'ghost', () => act('leave')));
      return;
    }
    if (g.phase !== 'playing') return;
    if (!myTurn()) {
      box.appendChild(el('div', 'note', 'Waiting for ' + seatName(S.seats, g.turn) + '…  (you can still arrange your cards)'));
      return;
    }
    if (g.turnStep === 'draw') {
      box.appendChild(button('Take pile', 'ghost', onDiscard, !g.canPickup));
      box.appendChild(button('Draw card', 'primary', onStock, !g.canDraw));
      return;
    }
    box.appendChild(button('Meld', '', doMeld, selected.size === 0));
    box.appendChild(button('Discard', 'primary', doDiscard, selected.size !== 1));
  }

  // ---- results

  function renderOverlay() {
    const g = S.game, ov = $('overlay'), panel = $('panel');
    if (g.phase === 'playing' || g.phase === 'idle') { if (!confirming) ov.classList.add('hidden'); return; }
    confirming = false;
    const r = g.result;
    if (!r) { ov.classList.add('hidden'); return; }
    panel.textContent = '';
    ov.classList.remove('hidden');
    if (g.phase === 'gameover') {
      const won = r.gameWinner === myTeam();
      panel.appendChild(el('h2', null, won ? 'Your team wins!' : teamName(S.seats, r.gameWinner) + ' win'));
      panel.appendChild(el('div', 'sub', 'First to ' + g.rules.targetScore + '.'));
    } else {
      const who = r.outSeat >= 0 ? (r.outSeat === you ? 'You' : seatName(S.seats, r.outSeat)) + ' went out' : 'The stock ran out';
      panel.appendChild(el('h2', null, 'Hand ' + r.handNo + ' complete'));
      panel.appendChild(el('div', 'sub', who + '. The team that did not deal counts first.'));
    }
    panel.appendChild(Q.resultTable(r, S.seats));
    const row = el('div', 'row');
    if (g.phase === 'handover') {
      panel.appendChild(el('div', 'sub', 'Next dealer: ' + seatName(S.seats, g.nextDealer)));
      row.appendChild(button('Deal next hand', '', () => act('next')));
    } else {
      row.appendChild(button('New game', '', () => act('newgame')));
    }
    panel.appendChild(row);
  }

  // ---------------------------------------------------------------- hand interaction
  // Tap = select / unselect. Press and drag = move the card (turns auto-sort off).

  const handBox = $('hand');

  function cardsInHand() { return [...handBox.querySelectorAll('.card')]; }

  // Where would a card dropped at (x, y) land? -> { id, after }
  function dropTarget(x, y, draggedId) {
    let best = null, bestD = Infinity;
    cardsInHand().forEach((c) => {
      if (+c.dataset.id === draggedId) return;
      const r = c.getBoundingClientRect();
      const cx = r.left + r.width / 2, cy = r.top + r.height / 2;
      const d = Math.hypot((x - cx) * 1, (y - cy) * 1.4);
      if (d < bestD) { bestD = d; best = { id: +c.dataset.id, after: x > cx, el: c }; }
    });
    return best;
  }

  function clearDropMarks() {
    handBox.querySelectorAll('.dropbefore,.dropafter').forEach((n) => n.classList.remove('dropbefore', 'dropafter'));
  }

  handBox.addEventListener('pointerdown', (e) => {
    const c = e.target.closest('.card');
    if (!c || drag || (e.pointerType === 'mouse' && e.button !== 0)) return;
    drag = { id: +c.dataset.id, el: c, x0: e.clientX, y0: e.clientY, started: false, pid: e.pointerId, target: null };
  });

  window.addEventListener('pointermove', (e) => {
    if (!drag || e.pointerId !== drag.pid) return;
    if (!drag.started) {
      if (Math.hypot(e.clientX - drag.x0, e.clientY - drag.y0) < 9) return;
      drag.started = true;
      if (autosort) {                       // taking manual control: freeze the sorted order
        order = displayCards().map((c) => c.id);
        autosort = false;
        store.set('autosort', '0');
        $('autosort').checked = false;
        handBox.classList.add('manual');
        Q.toast('Auto-sort off – arrange your cards as you like', 'info');
      }
      drag.el.classList.add('picked');
      const g = $('drag');
      g.textContent = '';
      g.appendChild(cardEl(handCards().find((c) => c.id === drag.id), { wild: false }));
      g.firstChild.style.setProperty('--cw', getComputedStyle(handBox).getPropertyValue('--cw') || '56px');
      g.classList.remove('hidden');
    }
    e.preventDefault();
    const g = $('drag');
    g.style.left = e.clientX + 'px';
    g.style.top = e.clientY + 'px';
    clearDropMarks();
    drag.target = dropTarget(e.clientX, e.clientY, drag.id);
    if (drag.target) drag.target.el.classList.add(drag.target.after ? 'dropafter' : 'dropbefore');
  }, { passive: false });

  function endDrag(e, cancelled) {
    if (!drag || e.pointerId !== drag.pid) return;
    const d = drag;
    drag = null;
    $('drag').classList.add('hidden');
    clearDropMarks();
    if (d.started) {
      if (!cancelled && d.target) {
        const ids = order.filter((id) => id !== d.id);
        let at = ids.indexOf(d.target.id);
        if (at < 0) at = ids.length;
        ids.splice(d.target.after ? at + 1 : at, 0, d.id);
        order = ids;
        saveOrder();
      }
      render();
      return;
    }
    if (cancelled) return;
    // a tap: toggle selection
    if (selected.has(d.id)) selected.delete(d.id); else selected.add(d.id);
    renderHand();
    renderActions();
    renderMelds();
  }
  window.addEventListener('pointerup', (e) => endDrag(e, false));
  window.addEventListener('pointercancel', (e) => endDrag(e, true));

  $('autosort').addEventListener('change', (e) => {
    autosort = e.target.checked;
    store.set('autosort', autosort ? '1' : '0');
    if (!autosort) { order = displayCardsSorted(); saveOrder(); }  // keep what you were looking at
    if (S) renderHand();
  });
  function displayCardsSorted() { return Q.sortCards(handCards()).map((c) => c.id); }

  $('clear').addEventListener('click', () => { selected.clear(); if (S) { renderHand(); renderActions(); renderMelds(); } });

  let resizeT = null;
  window.addEventListener('resize', () => {
    clearTimeout(resizeT);
    resizeT = setTimeout(() => { if (S && you >= 0 && !(drag && drag.started)) renderHand(); }, 120);
  });
})();
