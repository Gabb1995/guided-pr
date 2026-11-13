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

  function hunk(b, ctx) {
    const h = ctx.hunks[b.hunkId];
    const wrap = el('figure', 'blk blk-code');
    if (!h) { wrap.append(el('div', 'blk-missing', 'Unknown hunk ' + esc(b.hunkId))); return wrap; }
    wrap.append(head(b.label, h.path, `<span class="blk-stat"><b class="plus">+${h.additions}</b> <b class="minus">−${h.deletions}</b></span>`));
    const focus = new Set(b.focusKeys || []);
    if (b.side && b.startLine) for (let n = b.startLine; n <= (b.endLine || b.startLine); n++) focus.add(b.side + ':' + n);
    const table = el('div', 'diff');
    for (const r of h.rows) {
      const side = r.t === 'del' ? 'old' : 'new', n = side === 'old' ? r.o : r.n;
      const row = el('div', `dl ${r.t}${focus.has(side + ':' + n) ? ' focus' : ''}`,
        `<span class="ln">${r.o || ''}</span><span class="ln">${r.n || ''}</span><span class="sg">${r.t === 'add' ? '+' : r.t === 'del' ? '−' : ''}</span><code>${r.html || ' '}</code>`);
      Object.assign(row.dataset, { src: b.hunkId, hunk: b.hunkId, path: h.path, side, line: n });
      table.append(row);
    }
    wrap.append(table);
    return wrap;
  }

  function code(b) {
    const wrap = el('figure', 'blk blk-code');
    wrap.append(head(b.label, b.path, `<span class="blk-tag">${b.ref === 'base' ? 'before this change' : 'repo'} · outside the diff</span>`));
    const table = el('div', 'diff');
    const focus = new Set(b.focusKeys || []);
    for (const r of b.rows || []) {
      const row = el('div', `dl ctx${focus.has('new:' + r.n) ? ' focus' : ''}`, `<span class="ln"></span><span class="ln">${r.n}</span><span class="sg"></span><code>${r.html || esc(r.text) || ' '}</code>`);
      Object.assign(row.dataset, { src: `code:${b.ref}:${b.path}`, path: b.path, ref: b.ref, side: 'new', line: r.n });
      table.append(row);
    }
    wrap.append(table);
    return wrap;
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
    mermaid.render('mmd' + (++mid), b.mermaid).then(({ svg }) => { box.innerHTML = svg; }).catch(() => {});
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
      <div class="cm-actions"><span class="cm-note">Nothing is posted until you click Post.</span><button class="cm-discard">Discard</button><button class="cm-post">Post to ${esc(ctx.host)}</button></div>`;
    const ta = wrap.querySelector('textarea');
    ta.value = b.body;
    const fit = () => { ta.style.height = 'auto'; ta.style.height = ta.scrollHeight + 2 + 'px'; };
    requestAnimationFrame(fit); ta.oninput = fit;
    const actions = wrap.querySelector('.cm-actions'), note = wrap.querySelector('.cm-note');
    const posted = ctx.postedComment && ctx.postedComment(b);
    const markPosted = () => { ta.disabled = true; wrap.classList.add('posted'); wrap.querySelector('.cm-tag').textContent = 'Posted'; actions.innerHTML = `<span class="cm-note">Posted on ${esc(ctx.host)}.</span>`; };
    if (posted) { ta.value = posted.body; markPosted(); return wrap; }
    wrap.querySelector('.cm-discard').onclick = () => { wrap.classList.add('discarded'); ta.disabled = true; actions.innerHTML = '<span class="cm-note">Discarded.</span>'; };
    const post = wrap.querySelector('.cm-post');
    post.onclick = async () => {
      const body = ta.value.trim(); if (!body) return;
      post.disabled = true; note.className = 'cm-note'; note.textContent = 'Posting…';
      try { await ctx.postComment({ hunkId: b.hunkId, side: b.side, startLine: b.startLine, endLine: b.endLine, body }); markPosted(); }
      catch (e) { post.disabled = false; note.className = 'cm-note err'; note.textContent = e.message; }
    };
    return wrap;
  }

  function render(b, ctx) {
    switch (b && b.type) {
      case 'hunk': return hunk(b, ctx);
      case 'code': return code(b, ctx);
      case 'diagram': return diagram(b, ctx);
      case 'compare': return compare(b, ctx);
      case 'comment': return comment(b, ctx);
      default: return el('div', 'blk-missing', 'Unknown block ' + esc(b && b.type));
    }
  }

  window.Blocks = { render, md, inline, esc, el, short, VISUAL: new Set(['hunk', 'code', 'diagram', 'compare']) };
})();
