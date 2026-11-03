// Queens - spectator "table" screen. Shows the whole table: both teams' melds,
// the piles, whose turn it is, the dealer, scores. Never receives anyone's cards.
(function () {
  'use strict';
  const { el, cardEl, backEl, meldEl, teamName, seatName, SEAT_NAMES, RANK_ORDER } = Q;
  const $ = (id) => document.getElementById(id);

  let S = null;       // last state message from the server
  let info = null;    // /api/info (LAN addresses)

  const net = Q.connect({
    onMsg(m) {
      if (m.type === 'state') { S = m; render(); }
      else if (m.type === 'error') Q.toast(m.msg);
    },
    onClose() { Q.toast('Connection lost – reconnecting…', 'info'); },
  });

  fetch('/api/info').then((r) => r.json()).then((j) => { info = j; if (S) render(); }).catch(() => {});

  // ---------------------------------------------------------------- render

  function render() {
    renderScores();
    renderInfo();
    renderSeats();
    renderPiles();
    renderTrays();
    renderLog();
    renderHost();
    renderOverlay();
  }

  function renderScores() {
    const g = S.game;
    for (let t = 0; t < 2; t++) {
      const tv = g.teams[t], box = $('team' + t);
      box.textContent = '';
      box.classList.toggle('counts-first', g.phase === 'playing' && g.firstCount === t);
      box.appendChild(el('div', 'tname', teamName(S.seats, t)));
      box.appendChild(el('div', 'score', String(tv.score)));
      box.appendChild(el('div', 'tsub', g.phase === 'idle' ? 'Playing to ' + g.rules.targetScore
        : (tv.down ? 'Melds showing: ' + tv.meldPoints + ' pts' : 'Not down yet')));
      const badges = el('div', 'badges');
      if (g.phase !== 'idle' && g.firstCount === t) badges.appendChild(el('span', 'chip team', 'Counts first'));
      if (g.phase !== 'idle' && g.dealer % 2 === t) badges.appendChild(el('span', 'chip dark', 'Dealing'));
      if (g.phase !== 'idle' && tv.down) badges.appendChild(el('span', 'chip outline', 'Down'));
      box.appendChild(badges);
    }
  }

  function statusLine() {
    const g = S.game;
    if (g.phase === 'idle') return 'Waiting for players…';
    if (g.phase === 'handover') return 'Hand over';
    if (g.phase === 'gameover') return 'Game over';
    const seat = S.seats[g.turn];
    const who = seatName(S.seats, g.turn);
    if (seat && seat.taken && !seat.connected) return 'Waiting for ' + who + ' to reconnect…';
    return who + (g.turnStep === 'draw' ? ' is drawing' : ' is playing');
  }

  function renderInfo() {
    const g = S.game, box = $('info');
    box.textContent = '';
    box.appendChild(el('h1', null, 'Queens'));
    box.appendChild(el('div', 'status', statusLine()));
    if (g.phase !== 'idle') {
      box.appendChild(el('div', 'meta',
        'Hand ' + g.handNo + ' · Dealer ' + seatName(S.seats, g.dealer) +
        ' · Next dealer ' + seatName(S.seats, g.nextDealer) + ' · First to ' + g.rules.targetScore));
    }
  }

  function renderSeats() {
    const g = S.game;
    for (let i = 0; i < 4; i++) {
      const info = S.seats[i], slot = $('seat' + i);
      slot.textContent = '';
      const s = el('div', 'seat t' + (i % 2));
      if (!info.taken) s.classList.add('empty');
      if (info.taken && !info.connected) s.classList.add('offline');
      if (g.phase === 'playing' && g.turn === i) s.classList.add('turn');
      s.appendChild(el('div', 'sname', info.taken ? info.name : 'Empty seat'));
      s.appendChild(el('div', 'sdir', SEAT_NAMES[i]));
      const chips = el('div', 'schips');
      if (g.phase !== 'idle') {
        if (g.dealer === i) chips.appendChild(el('span', 'chip', 'Dealer'));
        if (g.nextDealer === i && g.phase !== 'idle') chips.appendChild(el('span', 'chip outline', 'Next dealer'));
      }
      s.appendChild(chips);
      const n = g.handCounts[i];
      if (g.phase !== 'idle') {
        const backs = el('div', 'backs');
        for (let k = 0; k < Math.min(n, 14); k++) backs.appendChild(backEl());
        s.appendChild(backs);
        s.appendChild(el('div', 'scount', n + (n === 1 ? ' card' : ' cards')));
      }
      slot.appendChild(s);
    }
  }

  function pile(label, count, cards, active) {
    const wrap = el('div', 'pilewrap');
    const p = el('div', 'pile');
    const inner = el('div', 'cards-wrap');
    if (!cards.length) inner.appendChild(el('div', 'empty'));
    cards.forEach((c, i) => {
      const e = c ? cardEl(c, { wild: Q.isWild(c, S.game.rules.jokersWild) }) : backEl();
      e.classList.add('stackcard');
      e.style.left = (i * 3) + 'px';
      e.style.top = (-i * 2) + 'px';
      inner.appendChild(e);
    });
    p.appendChild(inner);
    if (active) p.classList.add('active');
    if (count > 0) p.appendChild(el('div', 'count', String(count)));
    wrap.appendChild(p);
    wrap.appendChild(el('div', 'pilelabel', label));
    return wrap;
  }

  function renderPiles() {
    const g = S.game, box = $('piles');
    box.textContent = '';
    if (g.phase === 'idle') return;
    const backs = [];
    for (let i = 0; i < Math.min(3, Math.ceil(g.stockCount / 20)); i++) backs.push(null);
    box.appendChild(pile('Stock', g.stockCount, backs, false));
    // up to three most recent discards, newest on top
    const recent = (g.discardRecent || []).slice(-3);
    box.appendChild(pile('Discard', g.discardCount, recent, false));
  }

  function renderTrays() {
    const g = S.game;
    for (let s = 0; s < 4; s++) { const t = $('tray' + s); t.textContent = ''; t.style.removeProperty('--cw'); }
    if (g.phase === 'idle') return;
    for (let t = 0; t < 2; t++) {
      const tv = g.teams[t];
      if (tv.collector < 0 || !tv.melds.length) continue;
      const tray = $('tray' + tv.collector);
      tray.classList.add('t' + t);
      tray.classList.remove('t' + (1 - t));
      tray.appendChild(el('div', 'tlabel', seatName(S.seats, tv.collector) + ' keeps the melds'));
      // many melds: shrink the cards a little so they still fit in the space beside the piles
      if (tv.melds.length > 6) tray.style.setProperty('--cw', 'clamp(13px, calc(var(--u) * 1.6), 24px)');
      const melds = tv.melds.slice().sort((a, b) => RANK_ORDER.indexOf(a.rank) - RANK_ORDER.indexOf(b.rank));
      melds.forEach((m) => { const e = meldEl(m, g.rules.jokersWild); tray.appendChild(e); });
    }
  }

  function renderLog() {
    const box = $('log');
    box.textContent = '';
    (S.game.log || []).slice(-5).forEach((line) => box.appendChild(el('span', null, line)));
  }

  // ---------------------------------------------------------------- host controls & overlays

  function hostButton(label, type, cls, confirmMsg) {
    const b = el('button', 'btn small ' + (cls || ''), label);
    b.type = 'button';
    b.onclick = () => { if (confirmMsg && !confirm(confirmMsg)) return; net.send({ type }); };
    return b;
  }

  function renderHost() {
    const g = S.game, box = $('hostbtns');
    box.textContent = '';
    const full = S.seats.every((s) => s.taken);
    if (g.phase === 'idle' && !full) box.appendChild(hostButton('Fill empty seats with bots', 'fillbots', 'ghost'));
    if (g.phase === 'idle') { const b = hostButton('Deal first hand', 'start'); b.disabled = !full; box.appendChild(b); }
    if (g.phase === 'handover') box.appendChild(hostButton('Deal next hand', 'next'));
    if (g.phase === 'gameover') box.appendChild(hostButton('New game', 'newgame'));
    box.appendChild(hostButton('Reset table (clear seats)', 'reset', 'danger', 'Clear all seats and start over?'));
  }

  function joinUrl() {
    const h = location.hostname;
    if (h !== 'localhost' && h !== '127.0.0.1' && h !== '[::1]') return location.origin + '/play';
    if (info && info.urls && info.urls.length) return info.urls[0] + '/play';
    return location.origin + '/play';
  }

  function renderOverlay() {
    const g = S.game, ov = $('overlay'), panel = $('panel');
    panel.textContent = '';
    if (g.phase === 'playing') { ov.classList.add('hidden'); return; }
    ov.classList.remove('hidden');
    const full = S.seats.every((s) => s.taken);

    if (g.phase === 'idle') {
      panel.appendChild(el('h2', null, 'Queens'));
      panel.appendChild(el('div', 'sub', 'Four players, two teams. Partners sit across from each other.'));
      panel.appendChild(el('div', null, 'Join from a phone or tablet at'));
      panel.appendChild(el('div', 'joinurl', joinUrl()));
      const list = el('div', 'seatlist');
      S.seats.forEach((s, i) => {
        const d = el('div', 't' + (i % 2) + (s.taken ? '' : ' free'));
        d.appendChild(el('small', null, SEAT_NAMES[i] + ' · team ' + (i % 2 === 0 ? 'North/South' : 'East/West')));
        d.appendChild(el('span', null, s.taken ? s.name + (s.bot ? ' 🤖' : '') : 'waiting…'));
        list.appendChild(d);
      });
      panel.appendChild(list);
      const row = el('div', 'row');
      if (!full) row.appendChild(hostButton('Fill empty seats with bots', 'fillbots', 'ghost'));
      const start = hostButton('Deal first hand', 'start'); start.disabled = !full;
      row.appendChild(start);
      panel.appendChild(row);
      return;
    }

    const r = g.result;
    if (!r) { ov.classList.add('hidden'); return; }
    if (g.phase === 'gameover') {
      panel.appendChild(el('h2', null, teamName(S.seats, r.gameWinner) + ' win!'));
      panel.appendChild(el('div', 'sub', 'First to ' + g.rules.targetScore + ' — final hand below.'));
    } else {
      const who = r.outSeat >= 0 ? seatName(S.seats, r.outSeat) + ' went out' : 'The stock ran out';
      panel.appendChild(el('h2', null, 'Hand ' + r.handNo + ' complete'));
      panel.appendChild(el('div', 'sub', who + '. The team that did not deal counts first.'));
    }
    panel.appendChild(Q.resultTable(r, S.seats));
    const row = el('div', 'row');
    if (g.phase === 'handover') {
      panel.appendChild(el('div', 'sub', 'Next dealer: ' + seatName(S.seats, g.nextDealer)));
      row.appendChild(hostButton('Deal next hand', 'next'));
    } else {
      row.appendChild(hostButton('New game', 'newgame'));
    }
    panel.appendChild(row);
  }
})();
