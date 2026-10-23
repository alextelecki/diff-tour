// One step at a time, as in the TUI: ← → move, #k<n> in the address is the step shown.
// Without JavaScript every step stays visible, one under another.
(() => {
  const steps = () => [...document.querySelectorAll('.step')];
  const dark = matchMedia('(prefers-color-scheme: dark)').matches;
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: dark ? 'dark' : 'default' });
  let cur = 0;

  function show(i) {
    const all = steps();
    cur = Math.max(0, Math.min(all.length - 1, i));
    all.forEach((s, k) => { s.hidden = k !== cur; });
    const step = all[cur];
    document.querySelector('.progress i').style.width = ((cur + 1) * 100 / all.length) + '%';
    document.querySelector('.count').textContent = (cur + 1) + '/' + all.length;
    document.querySelector('.prev').disabled = cur === 0;
    document.querySelector('.next').disabled = cur === all.length - 1;
    document.querySelectorAll('.toc a').forEach(a => a.classList.toggle('current', a.dataset.chapter === step.dataset.chapter));
    history.replaceState(null, '', '#k' + (cur + 1));
    // Mermaid measures text, so a diagram is drawn only once its step is on screen
    const todo = step.querySelectorAll('.mermaid:not([data-processed])');
    if (todo.length) mermaid.run({ nodes: todo });
    window.scrollTo(0, 0);
  }

  const fromHash = () => { const m = /^#k(\d+)$/.exec(location.hash); return m ? Number(m[1]) - 1 : 0; };

  document.addEventListener('keydown', e => {
    if (e.target.closest?.('textarea, input') || e.altKey || e.ctrlKey || e.metaKey) return;
    if (e.key === 'ArrowRight') show(cur + 1);
    else if (e.key === 'ArrowLeft') show(cur - 1);
    else return;
    e.preventDefault();
  });
  document.addEventListener('click', e => {
    const t = e.target.closest('button, .toc a');
    if (!t) return;
    if (t.matches('.toc a')) {
      e.preventDefault();
      show(steps().findIndex(s => s.dataset.chapter === t.dataset.chapter));
    } else if (t.matches('.prev')) show(cur - 1);
    else if (t.matches('.next, .reveal')) show(cur + 1);
  });
  window.addEventListener('hashchange', () => show(fromHash()));

  // tour serve: a question goes to /ask, and the steps of the answer come back as HTML to put after this step
  document.addEventListener('click', e => {
    const b = e.target.closest('.suggest');
    if (!b) return;
    b.form.q.value = b.textContent;
    b.form.requestSubmit();
  });
  document.addEventListener('submit', async e => {
    const form = e.target.closest('form.ask');
    if (!form) return;
    e.preventDefault();
    const question = form.q.value.trim();
    if (!question) return;
    const step = form.closest('.step');
    const status = form.querySelector('.status');
    const buttons = form.querySelectorAll('button');
    const started = Date.now();
    const tick = () => { status.textContent = TOUR.answering + ' ' + Math.round((Date.now() - started) / 1000) + ' s'; };
    tick();
    const timer = setInterval(tick, 1000);
    buttons.forEach(b => { b.disabled = true; });
    try {
      const res = await fetch('/ask', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Tour-Token': TOUR.token },
        body: JSON.stringify({ after: steps().indexOf(step), n: steps().length, question }),
      });
      const body = await res.text();
      if (!res.ok) throw new Error(body.trim());
      const t = document.createElement('template');
      t.innerHTML = body;
      step.after(...t.content.querySelectorAll('.step'));
      form.reset();
      status.textContent = '';
      show(steps().indexOf(step) + 1);
    } catch (err) {
      status.textContent = err.message;
    } finally {
      clearInterval(timer);
      buttons.forEach(b => { b.disabled = false; });
    }
  });

  document.body.classList.add('js');
  show(fromHash());
})();
