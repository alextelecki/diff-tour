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

  document.body.classList.add('js');
  show(fromHash());
})();
