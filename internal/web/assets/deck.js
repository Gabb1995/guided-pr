// The review deck: an intro, then slides (code left, the author's narration right). Questions become detour
// slides placed after the slide they were asked on; answers stream in from the agent over server-sent events.
(function () {
  'use strict';
  const { render, md, esc, el, short, VISUAL } = Blocks;
  const $ = id => document.getElementById(id);
  const sessionId = location.pathname.split('/').filter(Boolean).pop();
  const api = p => `/api/sessions/${sessionId}${p}`;

  let V;                       // view: pullRequest, host, deck, hunks
  const track = [];            // intro, slides, minor screens, detours
  let i = 0, returnTo = null;
  const questions = new Map(); // qid -> {q, status, answer}
  const posted = new Map();    // "hunk|side|start|end" -> comment
  let lastSeq = 0, finished = false, listening = false, replaying = true;

  async function post(path, body) {
    const res = await fetch(api(path), { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Guided-PR': '1' }, body: JSON.stringify(body) });
    if (!res.ok) { let m = `HTTP ${res.status}`; try { m = (await res.json()).error || m; } catch {} throw new Error(m); }
    return res.status === 204 ? null : res.json();
  }

  const ctx = {
    get hunks() { return V.hunks; }, get host() { return V.host; },
    postComment: async c => { const ev = await post('/comments', c); applyEvent(ev); },
    postedComment: b => posted.get([b.hunkId, b.side, b.startLine, b.endLine].join('|')),
  };

  // ---------- track ----------
  function buildTrack() {
    track.push({ kind: 'intro', id: 'intro' });
    V.deck.chapters.forEach((c, ci) => {
      c.slides.forEach(s => track.push({ kind: 'slide', id: s.id, slide: s, chapter: c, ci }));
      if (c.minor && c.minor.length) track.push({ kind: 'minor', id: c.id + '--minor', chapter: c, ci });
    });
  }
  const indexOf = id => track.findIndex(t => t.id === id);
  // A question's origin is the planned slide it was asked on; follow-ups inherit their parent's origin.
  function originId(q) {
    if (q.parentId && questions.has(q.parentId)) return originId(questions.get(q.parentId).q);
    return q.slideId || 'intro';
  }
  function selectionOf(q) {
    if (q.selection) return q.selection;
    return q.parentId && questions.has(q.parentId) ? selectionOf(questions.get(q.parentId).q) : null;
  }
  function insertDetour(qid) {
    if (indexOf(qid) >= 0) return;
    const entry = questions.get(qid);
    let at = indexOf(originId(entry.q)) + 1;
    if (at <= 0) at = 1;
    while (track[at] && track[at].kind === 'answer') at++;
    track.splice(at, 0, { kind: 'answer', id: qid });
    if (at <= i && track.length > 1) i++;
    if (returnTo != null && at <= returnTo) returnTo++;
  }

  // ---------- events ----------
  function applyEvent(ev) {
    if (!ev) return;
    if (ev.seq) { if (ev.seq <= lastSeq) return; lastSeq = ev.seq; }
    const d = ev.data || {};
    switch (ev.type) {
      case 'question':
        if (questions.has(d.id)) return;
        questions.set(d.id, { q: d, status: '', answer: null });
        insertDetour(d.id);
        break;
      case 'status': { const e = questions.get(d.questionId); if (e) e.status = d.text; break; }
      case 'answer': {
        const e = questions.get(d.questionId); if (!e) break;
        e.answer = d.answer;
        if (!replaying && track[i] && track[i].id !== d.questionId) toast('Claude answered “' + trim(e.q.text, 48) + '”', () => go(indexOf(d.questionId)));
        break;
      }
      case 'comment': posted.set([d.hunkId, d.side, d.startLine, d.endLine].join('|'), d); updateCount(); break;
      case 'ended': finished = true; $('finish').textContent = 'Finished'; $('finish').classList.add('done'); break;
      case 'presence': listening = !!d.listening; updatePresence(); break;
    }
    if (ev.type !== 'presence' && ev.type !== 'comment') refreshIfShowing(ev);
  }
  function refreshIfShowing(ev) {
    const cur = track[i]; if (!cur) return;
    const qid = ev.data && (ev.data.questionId || ev.data.id);
    if (cur.kind === 'answer' && cur.id === qid) {
      if (ev.type === 'status') { const st = document.querySelector('#st'); if (st) { st.textContent = ev.data.text + '…'; return; } }
      show(false);
    } else progress();
  }
  function connect() {
    const es = new EventSource(api(`/events?after=${lastSeq}`));
    es.onmessage = m => { try { applyEvent(JSON.parse(m.data)); } catch {} };
    es.onerror = () => { es.close(); listening = false; updatePresence(); setTimeout(connect, 2000); };
  }

  // ---------- rendering ----------
  function progress() {
    const box = $('progress'); box.innerHTML = '';
    let seg = null;
    track.forEach((t, k) => {
      if (k === 0 || (t.kind === 'slide' && t.chapter.slides[0] === t.slide)) { seg = el('div', 'seg'); box.append(seg); }
      const b = el('button', (k < i ? 'done ' : '') + (k === i ? 'cur ' : '') + (t.kind === 'answer' ? 'detour' : ''));
      b.title = t.kind === 'intro' ? 'Intro' : t.kind === 'answer' ? questions.get(t.id).q.text : t.kind === 'minor' ? 'Small supporting changes' : t.slide.title;
      b.onclick = () => go(k);
      seg.append(b);
    });
  }

  function introView() {
    const d = V.deck, pr = V.pullRequest, sec = el('section', 'intro');
    const t = d.intro.ticket;
    sec.innerHTML = `<div class="eyebrow">${esc(V.host)} · !${pr.number}</div><h1>${esc(d.intro.title)}</h1><div class="story">${md(d.intro.story)}</div>
      <div class="meta">${t ? `<span>${t.url ? `<a href="${esc(t.url)}" target="_blank" rel="noreferrer">${esc(t.key)}</a>` : esc(t.key)}${t.title ? ' · ' + esc(t.title) : ''}</span>` : ''}
      ${pr.sourceBranch ? `<span>${esc(pr.sourceBranch)}</span>` : ''}<span>${esc(pr.headSha.slice(0, 10))}</span>
      <span>${d.chapters.length} chapter${d.chapters.length > 1 ? 's' : ''}</span></div>`;
    const start = el('button', 'start', 'Start the walkthrough →'); start.onclick = () => go(1); sec.append(start);
    return sec;
  }

  function visualBlocks(t) {
    if (t.kind === 'minor') return t.chapter.minor.map(h => ({ type: 'hunk', hunkId: h }));
    if (t.kind === 'slide') return t.slide.blocks.filter(b => VISUAL.has(b.type));
    return [];
  }
  function fillPane(pane, blocks) { pane.replaceChildren(...blocks.map(b => render(b, ctx))); }
  function withFocus(blocks, sel) {
    if (!sel) return blocks;
    const keys = [];
    for (let n = sel.startLine; n <= sel.endLine; n++) keys.push(sel.side + ':' + n);
    const mark = b => {
      if (b.type === 'compare') return { ...b, left: mark(b.left), right: mark(b.right) };
      const hit = (b.type === 'hunk' && b.hunkId === sel.hunkId) || (b.type === 'code' && !sel.hunkId && b.path === sel.path && b.ref === sel.ref);
      return hit ? { ...b, focusKeys: keys } : b;
    };
    return blocks.map(mark);
  }
  const selLabel = s => s ? `${short(s.path)}:${s.startLine}${s.endLine !== s.startLine ? '–' + s.endLine : ''}` : '';

  function slideView(t) {
    const sec = el('section', 'split');
    const left = el('div', 'pane code-pane'), right = el('div', 'pane text-pane');
    if (t.kind === 'answer') {
      const e = questions.get(t.id), sel = selectionOf(e.q);
      const origin = track[indexOf(originId(e.q))] || track[0];
      const originBlocks = withFocus(visualBlocks(origin), sel);
      const answerBlocks = e.answer ? (e.answer.blocks || []).filter(b => VISUAL.has(b.type)) : [];
      const tabs = el('div', 'tabs'), body = el('div');
      const opts = [];
      if (answerBlocks.length) opts.push([`Answer · ${answerBlocks.length} view${answerBlocks.length > 1 ? 's' : ''}`, answerBlocks, 'ans']);
      if (originBlocks.length) opts.push([sel ? 'Where you asked · ' + selLabel(sel) : 'Where you asked', originBlocks, 'org']);
      opts.forEach(([label, blocks, kind], n) => {
        const b = el('button', kind + (n === 0 ? ' on' : ''), esc(label));
        b.onclick = () => { tabs.querySelectorAll('button').forEach(x => x.classList.remove('on')); b.classList.add('on'); fillPane(body, blocks); };
        tabs.append(b);
      });
      if (opts.length) { left.append(tabs, body); fillPane(body, opts[0][1]); }
      const on = origin.kind === 'slide' ? origin.slide.title : origin.kind === 'minor' ? 'Small supporting changes' : 'the intro';
      right.innerHTML = `<div class="eyebrow"><span class="detour-tag">Your question</span><span>on “${esc(on)}”</span>${sel ? `<span class="sel-tag">${esc(selLabel(sel))}</span>` : ''}</div><h1>${esc(e.q.text)}</h1>`;
      if (!e.answer) {
        right.insertAdjacentHTML('beforeend', `<div class="thinking"><span class="dot"></span><span id="st">${esc(e.status || (listening ? 'Claude is looking into it' : 'Waiting for Claude'))}…</span></div>`);
        if (!listening) right.insertAdjacentHTML('beforeend', '<p class="waiting-note">Claude isn’t connected right now. Your question is saved and will be answered when the review session in your terminal is listening again.</p>');
      } else {
        right.insertAdjacentHTML('beforeend', `<div class="say">${md(e.answer.say)}</div>`);
        (e.answer.blocks || []).filter(b => !VISUAL.has(b.type)).forEach(b => right.append(render(b, ctx)));
      }
      const back = el('button', 'back', '← Back to the walkthrough <kbd>Esc</kbd>'); back.onclick = leaveDetour; right.append(back);
    } else {
      fillPane(left, visualBlocks(t));
      const title = t.kind === 'minor' ? 'Small supporting changes' : t.slide.title;
      const say = t.kind === 'minor' ? 'The rest of this chapter is small stuff: imports, renames, fixtures. Collected here so you don’t have to step through it.' : t.slide.say;
      right.innerHTML = `<div class="eyebrow">Chapter ${t.ci + 1} · ${esc(t.chapter.title)}</div><h1>${esc(title)}</h1><div class="say">${md(say)}</div>`;
      if (t.kind === 'slide') t.slide.blocks.filter(b => !VISUAL.has(b.type)).forEach(b => right.append(render(b, ctx)));
    }
    if (!left.childElementCount) sec.classList.add('text-only');
    sec.append(left, right);
    return sec;
  }

  function show(resetScroll = true) {
    const t = track[i];
    const panes = [...document.querySelectorAll('#main .pane')].map(p => p.scrollTop);
    $('main').replaceChildren(t.kind === 'intro' ? introView() : slideView(t));
    $('main').classList.toggle('scroll', t.kind === 'intro');
    if (!resetScroll) document.querySelectorAll('#main .pane').forEach((p, k) => { p.scrollTop = panes[k] || 0; });
    $('prev').disabled = i === 0; $('next').disabled = i === track.length - 1;
    sel = null; anchor = null; setCtx(); progress();
  }
  function go(k) { if (k < 0 || k >= track.length) return; i = k; show(); }
  function leaveDetour() { if (returnTo != null) { const r = returnTo; returnTo = null; go(r); } else go(i + 1); }

  // ---------- selection: drag or shift-click rows in the code pane ----------
  let sel = null, anchor = null, dragging = false;
  const rowsOf = src => [...document.querySelectorAll('.code-pane .dl[data-src]')].filter(r => r.dataset.src === src);
  function rangeRows(a, b) { const all = rowsOf(a.dataset.src); const x = all.indexOf(a), y = all.indexOf(b); return all.slice(Math.min(x, y), Math.max(x, y) + 1); }
  function applySel(rows) {
    document.querySelectorAll('.code-pane .dl.sel').forEach(r => r.classList.remove('sel'));
    document.querySelector('.sel-pop')?.remove();
    if (!rows.length) { sel = null; setCtx(); return; }
    rows.forEach(r => r.classList.add('sel'));
    // A range must stay on one side of the diff, so a comment can be anchored.
    const side = rows[rows.length - 1].dataset.side;
    const same = rows.filter(r => r.dataset.side === side);
    const d = same[0].dataset, lines = same.map(r => +r.dataset.line);
    sel = { hunkId: d.hunk || undefined, path: d.path, ref: d.ref || undefined, side, startLine: Math.min(...lines), endLine: Math.max(...lines),
      code: rows.map(r => r.querySelector('code').textContent).join('\n') };
    setCtx(); popup(rows[rows.length - 1]);
  }
  function popup(lastRow) {
    const pane = lastRow.closest('.code-pane'); if (!pane) return;
    const pop = el('div', 'sel-pop', `<span>${esc(selLabel(sel))}</span><button class="askb">Ask</button>${sel.hunkId ? '<button class="cmb">Comment</button>' : ''}<button class="x" title="Clear (Esc)">✕</button>`);
    const r = lastRow.getBoundingClientRect(), pr = pane.getBoundingClientRect();
    pop.style.top = (r.bottom - pr.top + pane.scrollTop - r.height / 2 - 14) + 'px';
    pop.style.right = (pr.right - r.right + 12) + 'px';
    pop.querySelector('.askb').onclick = () => $('q').focus();
    const cmb = pop.querySelector('.cmb'); if (cmb) cmb.onclick = () => { $('q').value = 'Leave a comment: '; $('q').focus(); };
    pop.querySelector('.x').onclick = () => applySel([]);
    pane.append(pop);
  }
  document.addEventListener('mousedown', e => {
    const row = e.target.closest('.code-pane .dl[data-src]'); if (!row || e.button !== 0) return;
    if (e.shiftKey && anchor && anchor.isConnected && anchor.dataset.src === row.dataset.src) { e.preventDefault(); applySel(rangeRows(anchor, row)); return; }
    if (sel && row.classList.contains('sel') && document.querySelectorAll('.dl.sel').length === 1) { applySel([]); anchor = null; return; }
    anchor = row; dragging = true; document.body.classList.add('dragging');
    document.querySelectorAll('.code-pane .dl.sel').forEach(r => r.classList.remove('sel')); row.classList.add('sel');
  });
  document.addEventListener('mouseover', e => {
    if (!dragging) return;
    const row = e.target.closest('.code-pane .dl[data-src]');
    if (row && row.dataset.src === anchor.dataset.src) { document.querySelectorAll('.code-pane .dl.sel').forEach(r => r.classList.remove('sel')); rangeRows(anchor, row).forEach(r => r.classList.add('sel')); }
  });
  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false; document.body.classList.remove('dragging');
    applySel([...document.querySelectorAll('.code-pane .dl.sel')]);
  });

  function setCtx() {
    const c = $('ctx'), q = $('q'), t = track[i];
    c.classList.toggle('sel', !!sel);
    if (sel) { c.textContent = selLabel(sel) + ' ✕'; c.title = 'Clear selection'; c.onclick = () => applySel([]); q.placeholder = 'Ask about the selected lines, or “leave a comment…”'; return; }
    c.onclick = null; c.title = ''; q.placeholder = 'Ask about this slide…';
    if (!t) return;
    if (t.kind === 'intro') c.textContent = 'Whole MR';
    else if (t.kind === 'answer') c.textContent = 'Follow-up';
    else { const h = visualBlocks(t).find(b => b.type === 'hunk'); c.textContent = h ? short(V.hunks[h.hunkId].path) : t.chapter.title; }
  }

  // ---------- asking ----------
  async function ask(text) {
    const cur = track[i];
    const q = { text, selection: sel || undefined };
    if (cur.kind === 'answer') q.parentId = cur.id;
    else q.slideId = cur.kind === 'slide' ? cur.slide.id : cur.kind === 'minor' ? cur.id : '';
    if (cur.kind !== 'answer') returnTo = i;
    try {
      const created = await post('/questions', q);
      applyEvent({ seq: 0, type: 'question', data: created });
      go(indexOf(created.id));
    } catch (e) { toast('Could not send the question: ' + e.message); }
  }

  // ---------- chrome ----------
  function updatePresence() {
    const p = $('presence'); p.classList.toggle('on', listening);
    p.title = listening ? 'Claude is listening' : 'Claude isn’t connected. Questions are saved and answered once the session in your terminal listens again.';
  }
  function updateCount() { const n = posted.size; $('cmCount').hidden = !n; $('cmCount').textContent = `${n} comment${n > 1 ? 's' : ''} posted`; }
  let toastTimer;
  function toast(text, action) {
    const t = $('toast'); t.innerHTML = `<span>${esc(text)}</span>`;
    if (action) { const b = el('button', null, 'Show'); b.onclick = () => { t.hidden = true; action(); }; t.append(b); }
    t.hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { t.hidden = true; }, 6000);
  }
  const trim = (s, n) => s.length > n ? s.slice(0, n - 1) + '…' : s;

  $('askForm').onsubmit = e => { e.preventDefault(); const v = $('q').value.trim(); if (!v) return; $('q').value = ''; $('q').blur(); ask(v); };
  $('prev').onclick = () => go(i - 1); $('next').onclick = () => go(i + 1);
  $('finish').onclick = async () => {
    if (finished || !confirm('Finish the review? Claude stops listening for questions.')) return;
    try { await post('/end', {}); } catch (e) { toast(e.message); }
  };
  addEventListener('keydown', e => {
    if (e.target.matches('input, textarea')) { if (e.key === 'Escape') e.target.blur(); return; }
    if (e.key === 'ArrowRight' || e.key === 'j') go(i + 1);
    else if (e.key === 'ArrowLeft' || e.key === 'k') go(i - 1);
    else if (e.key === '/') { e.preventDefault(); $('q').focus(); }
    else if (e.key === 'Escape' && sel) applySel([]);
    else if (e.key === 'Escape' && track[i] && track[i].kind === 'answer') leaveDetour();
  });

  (async function init() {
    const res = await fetch(api('/view'));
    if (!res.ok) { let m = `HTTP ${res.status}`; try { m = (await res.json()).error || m; } catch {} $('main').innerHTML = `<div class="error">${esc(m)}</div>`; return; }
    V = await res.json();
    document.title = V.pullRequest.title;
    $('prTitle').textContent = V.pullRequest.title; $('prTitle').href = V.pullRequest.htmlUrl;
    $('prMeta').textContent = `${V.pullRequest.owner}/${V.pullRequest.repo} !${V.pullRequest.number} · ${V.pullRequest.headSha.slice(0, 10)}`;
    buildTrack();
    listening = V.listening; updatePresence();
    (V.events || []).forEach(applyEvent);
    replaying = false;
    show();
    connect();
  })();
})();
