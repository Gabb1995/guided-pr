// Renders review-deck.v1 blocks. Slides and answers use the same blocks.
(function () {
  'use strict';
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const inline = s => esc(s)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[\s(])[_*]([^_*\s][^_*]*)[_*](?=[\s).,:;!?]|$)/g, '$1<em>$2</em>');
  // Paragraphs separated by blank lines; single newlines become spaces.
  const md = s => String(s ?? '').trim().split(/\n\s*\n/).map(p => `<p>${inline(p.replace(/\n/g, ' '))}</p>`).join('');
  const el = (tag, cls, html) => { const e = document.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; };
  const short = p => String(p || '').split('/').slice(-2).join('/');

  function head(label, path, extra) {
    return el('div', 'blk-head', (label ? `<span class="blk-label">${esc(label)}</span>` : '') +
      `<span class="blk-path" title="${esc(path)}">‎${esc(path)}</span>` + (extra || ''));
  }

  // Notes keyed "side:line"; hunks default to the new side, code blocks only have it.
  function notesBy(b) {
    const m = new Map();
    for (const n of b.notes || []) {
      const k = (n.side || 'new') + ':' + n.line;
      m.set(k, [...(m.get(k) || []), n.text]);
    }
    return m;
  }
  const noteRows = texts => (texts || []).map(t => el('div', 'dl-note', `<span class="note-mark">▲</span><span>${inline(t)}</span>`));

  function hunk(b, ctx) {
    const h = ctx.hunks[b.hunkId];
    const wrap = el('figure', 'blk blk-code');
    if (!h) { wrap.append(el('div', 'blk-missing', 'Unknown hunk ' + esc(b.hunkId))); return wrap; }
    wrap.append(head(b.label, h.path, `<span class="blk-stat"><b class="plus">+${h.additions}</b> <b class="minus">−${h.deletions}</b></span>`));
    const focus = new Set(b.focusKeys || []);
    if (b.side && b.startLine) for (let n = b.startLine; n <= (b.endLine || b.startLine); n++) focus.add(b.side + ':' + n);
    const table = el('div', 'diff'), notes = notesBy(b);
    for (const r of h.rows) {
      const side = r.t === 'del' ? 'old' : 'new', n = side === 'old' ? r.o : r.n;
      const row = el('div', `dl ${r.t}${focus.has(side + ':' + n) ? ' focus' : ''}`,
        `<span class="ln">${r.o || ''}</span><span class="ln">${r.n || ''}</span><span class="sg">${r.t === 'add' ? '+' : r.t === 'del' ? '−' : ''}</span><code>${r.html || ' '}</code>`);
      Object.assign(row.dataset, { src: b.hunkId, hunk: b.hunkId, path: h.path, side, line: n });
      table.append(row, ...noteRows(notes.get(side + ':' + n)));
    }
    wrap.append(table);
    return wrap;
  }

  function code(b) {
    const wrap = el('figure', 'blk blk-code');
    wrap.append(head(b.label, b.path, `<span class="blk-tag">${b.ref === 'base' ? 'before this change' : 'repo'} · outside the diff</span>`));
    const table = el('div', 'diff'), notes = notesBy(b);
    const focus = new Set(b.focusKeys || []);
    for (const r of b.rows || []) {
      const row = el('div', `dl ctx${focus.has('new:' + r.n) ? ' focus' : ''}`, `<span class="ln"></span><span class="ln">${r.n}</span><span class="sg"></span><code>${r.html || esc(r.text) || ' '}</code>`);
      Object.assign(row.dataset, { src: `code:${b.ref}:${b.path}`, path: b.path, ref: b.ref, side: 'new', line: r.n });
      table.append(row, ...noteRows(notes.get('new:' + r.n)));
    }
    wrap.append(table);
    return wrap;
  }

  // A classDef/style that sets fill: but not color: keeps the theme's text colour,
  // e.g. light text on a pale fill in dark mode. Pick dark or light text by the fill's luminance.
  function fixNodeContrast(box) {
    const lum = (c) => {
      const m = c && c.match(/rgba?\(([^)]+)\)/);
      if (!m) return null;
      const [r, g, b, a = 1] = m[1].split(',').map(Number);
      if (a === 0) return null;
      const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    box.querySelectorAll('g.node').forEach((node) => {
      const shape = node.querySelector('rect, polygon, circle, ellipse, path');
      if (!shape || !/fill\s*:/.test(shape.getAttribute('style') || '')) return;
      const L = lum(getComputedStyle(shape).fill);
      if (L == null) return;
      const text = L > 0.4 ? '#1a1a1a' : '#f2f2f2';
      node.querySelectorAll('text, tspan, span, div, p').forEach((t) => {
        t.style.setProperty('color', text, 'important');
        t.style.setProperty('fill', text, 'important');
      });
    });
  }

  let mermaidReady = false, mid = 0;
  function diagram(b) {
    const wrap = el('figure', 'blk blk-diagram');
    const box = el('div', 'mermaid-box', '<pre></pre>');
    box.firstChild.textContent = b.mermaid;
    wrap.append(box);
    if (b.caption) wrap.append(el('figcaption', null, inline(b.caption)));
    if (!window.mermaid) return wrap;
    if (!mermaidReady) {
      const dark = matchMedia('(prefers-color-scheme: dark)').matches;
      mermaid.initialize({ startOnLoad: false, theme: dark ? 'dark' : 'neutral', securityLevel: 'strict', fontFamily: 'Inter, system-ui, sans-serif' });
      mermaidReady = true;
    }
    mermaid.render('mmd' + (++mid), b.mermaid).then(({ svg }) => { box.innerHTML = svg; fixNodeContrast(box); }).catch(() => {});
    return wrap;
  }

  function table(b) {
    const wrap = el('figure', 'blk blk-table');
    const scroll = el('div', 'table-scroll');
    const t = el('table');
    t.innerHTML = `<thead><tr>${(b.columns || []).map(c => `<th>${inline(c)}</th>`).join('')}</tr></thead>` +
      `<tbody>${(b.cells || []).map(r => `<tr>${r.map(c => `<td>${inline(c)}</td>`).join('')}</tr>`).join('')}</tbody>`;
    scroll.append(t);
    wrap.append(scroll);
    if (b.caption) wrap.append(el('figcaption', null, inline(b.caption)));
    return wrap;
  }

  function compare(b, ctx) {
    const wrap = el('figure', 'blk blk-compare');
    const grid = el('div', 'compare-grid');
    grid.append(render(b.left, ctx), render(b.right, ctx));
    wrap.append(grid);
    if (b.caption) wrap.append(el('figcaption', null, inline(b.caption)));
    return wrap;
  }

  // A draft comment the agent wrote. Only the reviewer can post it.
  function comment(b, ctx) {
    const h = ctx.hunks[b.hunkId];
    const where = `${h ? h.path : '?'}:${b.startLine}${b.endLine !== b.startLine ? '–' + b.endLine : ''}`;
    const wrap = el('div', 'blk blk-comment');
    wrap.innerHTML = `<div class="cm-head"><span class="cm-tag">Draft comment</span><span class="cm-where" title="${esc(where)}">${esc(where)}</span></div>
      <textarea rows="3"></textarea>
      <div class="cm-actions"><span class="cm-note">Queued comments are sent to ${esc(ctx.host)} together when you finish.</span><button class="cm-discard">Discard</button><button class="cm-post">Add to review</button></div>`;
    const ta = wrap.querySelector('textarea');
    ta.value = b.body;
    const fit = () => { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 2 + 'px'; };
    requestAnimationFrame(fit); ta.oninput = fit;
    const actions = wrap.querySelector('.cm-actions'), note = wrap.querySelector('.cm-note');
    const done = ctx.commentState && ctx.commentState(b);
    const mark = state => {
      ta.disabled = true; wrap.classList.add('posted');
      wrap.querySelector('.cm-tag').textContent = state === 'sent' ? 'Sent' : 'Queued';
      actions.innerHTML = `<span class="cm-note">${state === 'sent' ? `Sent to ${esc(ctx.host)}.` : 'In your review. You can edit or drop it when you finish.'}</span>`;
    };
    if (done) { ta.value = done.body; mark(done.state); return wrap; }
    if (ctx.reviewSent && ctx.reviewSent()) { ta.disabled = true; actions.innerHTML = '<span class="cm-note">The review was already sent.</span>'; return wrap; }
    wrap.querySelector('.cm-discard').onclick = () => { wrap.classList.add('discarded'); ta.disabled = true; actions.innerHTML = '<span class="cm-note">Discarded.</span>'; };
    const post = wrap.querySelector('.cm-post');
    post.onclick = async () => {
      const body = ta.value.trim(); if (!body) return;
      post.disabled = true; note.className = 'cm-note'; note.textContent = 'Adding…';
      try { await ctx.queueComment({ hunkId: b.hunkId, side: b.side, startLine: b.startLine, endLine: b.endLine, body }); mark('queued'); }
      catch (e) { post.disabled = false; note.className = 'cm-note err'; note.textContent = e.message; }
    };
    return wrap;
  }

  function render(b, ctx) {
    switch (b && b.type) {
      case 'hunk': return hunk(b, ctx);
      case 'code': return code(b, ctx);
      case 'diagram': return diagram(b, ctx);
      case 'table': return table(b, ctx);
      case 'compare': return compare(b, ctx);
      case 'comment': return comment(b, ctx);
      default: return el('div', 'blk-missing', 'Unknown block ' + esc(b && b.type));
    }
  }

  window.Blocks = { render, md, inline, esc, el, short, VISUAL: new Set(['hunk', 'code', 'diagram', 'table', 'compare']) };
})();
