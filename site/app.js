(() => {
  const $ = (id) => document.getElementById(id);
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const lerp = (a, b, t) => a + (b - a) * t;
  const clamp = (v, a = 0, b = 1) => Math.min(b, Math.max(a, v));
  const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
  const k = (v) => (v < 0 ? '-$' : '$') + Math.abs(v).toFixed(1) + 'k';

  /* ---------- smooth scrolling ----------
     Native scrolling, with CSS smooth anchors. The chart eases toward the scroll position each frame
     (below), which gives the smooth zoom without hijacking the wheel. */
  document.querySelectorAll('a[href^="#"]').forEach((a) => a.addEventListener('click', (e) => {
    const id = a.getAttribute('href');
    const target = id === '#top' ? document.body : document.querySelector(id);
    if (!target) return;
    e.preventDefault();
    target.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth' });
  }));

  /* ---------- the story: 200 days of AI gains, a runaway agent, and the Pitstop line ---------- */
  let seed = 11;
  const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  const D = 200, PEAK = 150, CRASH_END = 172, PAUSE = 147;
  const day0 = Date.UTC(2026, 4, 8) / 1000;
  const T = (i) => day0 + i * 86400;
  const spend = [], gain = [], saved = [];
  let g = 20;
  for (let i = 0; i < D; i++) {
    let s = 2 + rnd() * 1.6; // normal AI spend, $k per day
    if (i >= PEAK && i < CRASH_END) s += 4 + 0.6 * Math.pow(1.15, i - PEAK); // a looping agent nobody stopped
    if (i >= CRASH_END) s = 4.6 + rnd() * 1.2; // bill still high after the damage
    spend.push(s);
    const earned = 4.8 + Math.sin(i / 8) * 2.2 + (rnd() - 0.5) * 3.4;
    g += earned - s;
    gain.push(g);
  }
  let h = gain[PAUSE];
  for (let i = PAUSE; i < D; i++) {
    const s = i === PAUSE ? 0 : 2 + rnd() * 1.6;
    h += i === PAUSE ? 0 : 4.8 + Math.sin(i / 8) * 2.2 + (rnd() - 0.5) * 3.4 - s;
    saved.push({ i, v: h, s });
  }
  const peakV = Math.max(...gain.slice(0, PEAK));

  /* ---------- chart: TradingView Lightweight Charts ---------- */
  const LWC = window.LightweightCharts;
  let chart, up, dn, ps, vol, manual = null, last = { up: -1, dn: -1, ps: -1 };
  if (LWC && $('tv')) {
    document.querySelector('.plot').classList.add('tv-on');
    chart = LWC.createChart($('tv'), {
      autoSize: true,
      layout: { background: { type: 'solid', color: '#000000' }, textColor: '#a8a8a8', fontFamily: 'Figtree, system-ui, sans-serif', fontSize: 12 },
      grid: { vertLines: { color: '#161616' }, horzLines: { color: '#161616' } },
      rightPriceScale: { borderVisible: false, scaleMargins: { top: 0.1, bottom: 0.03 } },
      timeScale: { borderVisible: false, rightOffset: 3 },
      crosshair: { mode: 0, vertLine: { color: '#5a5a5a', labelBackgroundColor: '#2a2a2a' }, horzLine: { color: '#5a5a5a', labelBackgroundColor: '#2a2a2a' } },
      handleScroll: false, handleScale: false,
      localization: { priceFormatter: k },
    });
    const top = Math.max(peakV, ...saved.map((p) => p.v)) + 20;
    const fixed = { autoscaleInfoProvider: () => ({ priceRange: { minValue: Math.max(0, Math.min(...gain) - 10), maxValue: top } }) };
    up = chart.addAreaSeries({ lineColor: '#22ab94', topColor: 'rgba(34,171,148,0.40)', bottomColor: 'rgba(34,171,148,0.02)', lineWidth: 2, ...fixed });
    dn = chart.addAreaSeries({ lineColor: '#f23645', topColor: 'rgba(242,54,69,0.36)', bottomColor: 'rgba(242,54,69,0.02)', lineWidth: 2, ...fixed });
    ps = chart.addLineSeries({ color: '#f0a030', lineWidth: 3, ...fixed });
    vol = chart.addHistogramSeries({ priceScaleId: 'vol', priceFormat: { type: 'custom', formatter: k }, lastValueVisible: false, priceLineVisible: false });
    chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.84, bottom: 0 } });
    document.querySelectorAll('.tabs button').forEach((b) => b.addEventListener('click', () => {
      const n = +b.dataset.n;
      document.querySelectorAll('.tabs button').forEach((x) => x.setAttribute('aria-pressed', x === b));
      const end = Math.max(last.up, last.dn, last.ps, 10);
      manual = n ? { from: end - n, to: end + 3 } : { from: -2, to: D + 2 };
      chart.timeScale().setVisibleLogicalRange(manual);
    }));
  }

  const volBar = (i) => ({ time: T(i), value: spend[i], color: i > PEAK ? 'rgba(242,54,69,0.75)' : 'rgba(120,120,120,0.5)' });

  /* ---------- scroll-linked zoom ---------- */
  const wrap = $('chart');
  let phase = -1, ticking = false;
  const caps = [
    'Your agents ship work. The gains add up.',
    'Then one agent loops all weekend. The bill eats the gains.',
    'With Pitstop, that run pauses at the top and asks you first.',
  ];
  function render(t) {
    if (!chart) return;
    const tUp = ease(clamp(t / 0.42));
    const tDn = clamp((t - 0.46) / 0.22);
    const tPs = clamp((t - 0.72) / 0.18);
    const rUp = Math.round(lerp(8, PEAK, tUp));
    const rDn = tDn > 0 ? Math.round(lerp(PEAK, D - 1, tDn)) : -1;
    const rPs = tPs > 0 ? Math.round(lerp(PAUSE, D - 1, tPs)) : -1;

    if (rUp !== last.up) { up.setData(gain.slice(0, rUp + 1).map((v, i) => ({ time: T(i), value: v }))); last.up = rUp; }
    if (rDn !== last.dn) {
      dn.setData(rDn < 0 ? [] : gain.slice(PEAK, rDn + 1).map((v, j) => ({ time: T(PEAK + j), value: v })));
      dn.setMarkers(rDn > PEAK + 10 ? [{ time: T(PEAK + 9), position: 'aboveBar', color: '#f23645', shape: 'arrowDown', text: 'Agent loops' }] : []);
      last.dn = rDn;
    }
    if (rPs !== last.ps) {
      ps.setData(rPs < 0 ? [] : saved.filter((p) => p.i <= rPs).map((p) => ({ time: T(p.i), value: p.v })));
      ps.setMarkers(rPs < 0 ? [] : [{ time: T(PAUSE), position: 'aboveBar', color: '#f0a030', shape: 'arrowDown', text: 'Pitstop paused here' }]);
      last.ps = rPs;
    }
    const rEnd = Math.max(rUp, rDn);
    vol.setData(Array.from({ length: rEnd + 1 }, (_, i) => volBar(i)));

    // zoom: whole history first, closing in on the peak as you scroll, then widening to show both endings
    if (manual && t > 0.02 && t < 0.98) manual = null;
    if (!manual) {
      const z = ease(clamp(t / 0.6));
      const zOut = ease(clamp((t - 0.72) / 0.22));
      chart.timeScale().setVisibleLogicalRange({
        from: lerp(lerp(-2, 96, z), 110, zOut),
        to: lerp(lerp(D + 2, Math.max(rUp, rDn, 150) + 8, z), D + 2, zOut),
      });
    }

    // header quote, TradingView style
    const showPs = rPs >= 0 && tPs > 0.3;
    const at = (i) => saved.find((p) => p.i === i);
    const cur = showPs ? at(rPs).v : rDn >= 0 ? gain[rDn] : gain[rUp];
    const prev = showPs ? at(Math.max(PAUSE, rPs - 1)).v : rDn > PEAK ? gain[rDn - 1] : gain[Math.max(0, rUp - 1)];
    const ch = cur - prev, pct = (ch / Math.max(1, Math.abs(prev))) * 100;
    $('tk-spent').textContent = k(cur);
    const chg = $('tk-chg');
    chg.textContent = `${ch >= 0 ? '+' : '-'}${k(Math.abs(ch))} (${ch >= 0 ? '+' : ''}${pct.toFixed(2)}%)`;
    chg.classList.toggle('pos', ch >= 0);
    $('tk-calls').textContent = k(showPs ? at(rPs).s : spend[rEnd]);
    $('tk-steps').textContent = showPs ? '2, 1 paused' : rDn > PEAK ? '3, 1 looping' : '3';
    const st = $('tk-last');
    st.className = showPs ? 'hold' : rDn > PEAK ? 'bad' : 'up';
    st.textContent = showPs ? 'loop paused' : rDn > PEAK ? 'bill climbing' : 'growing';

    $('card').classList.toggle('show', t > 0.86);
    const ph = tPs > 0.15 ? 2 : tDn > 0.1 ? 1 : 0;
    if (ph !== phase) { phase = ph; $('cap').textContent = caps[ph]; }
  }
  // Ease the chart toward the scroll position so wheel steps become one smooth zoom.
  let shown = 0, goal = 0;
  const progress = () => { const r = wrap.getBoundingClientRect(); return clamp(-r.top / (r.height - innerHeight)); };
  function frame() {
    const d = goal - shown;
    shown = Math.abs(d) < 0.0005 ? goal : shown + d * 0.14;
    render(shown);
    ticking = shown !== goal;
    if (ticking) requestAnimationFrame(frame);
  }
  function onScroll() {
    goal = reduce ? 1 : progress();
    if (reduce) { shown = goal; render(goal); return; }
    if (!ticking) { ticking = true; requestAnimationFrame(frame); }
  }
  addEventListener('scroll', onScroll, { passive: true });
  addEventListener('resize', onScroll);
  shown = goal = reduce ? 1 : progress(); // start where the page is, no sweep on reload
  render(shown);

  /* ---------- decision card ---------- */
  $('c-go').addEventListener('click', () => { $('c-note').textContent = 'Budget raised to $18.50. The agent resumes in seconds.'; });
  $('c-stop').addEventListener('click', () => { $('c-note').textContent = 'Saved to pitstop/wip/1124-2050. Resume: pitstop resume 1124-2050'; });

  /* ---------- hero terminal ---------- */
  const lines = [
    ['$ pitstop run -- claude', ''],
    ['pitstop run 1124-2050  repo=checkout-service branch=main', 'dim'],
    ['> Add retries to the payment webhook', ''],
    ['  done  Read webhook handler', 'dim'],
    ['  done  Write failing test', 'dim'],
    ['  done  Add backoff helper', 'dim'],
    ['  now   Wire retries into handler', 'hi'],
  ];
  const typed = $('typed');
  const status = [[0.21, 0], [0.58, 1], [1.37, 2], [2.40, 3], [3.40, 3]];
  const money = (v) => '$' + v.toFixed(2);
  async function typeAll() {
    for (let i = 0; i < lines.length; i++) {
      const [txt, cls] = lines[i];
      const div = document.createElement('div');
      if (cls) div.className = cls;
      typed.append(div);
      if (reduce || i > 0) div.textContent = txt;
      else for (let c = 1; c <= txt.length; c++) { div.textContent = txt.slice(0, c); await new Promise((r) => setTimeout(r, 38)); }
      if (i >= 2) {
        const [s, st] = status[Math.min(i - 2, status.length - 1)];
        $('st-spent').textContent = money(s); $('st-steps').textContent = `${st}/8`;
      }
      if (!reduce) await new Promise((r) => setTimeout(r, i === 0 ? 400 : 650));
    }
    const cur = document.createElement('span'); cur.className = 'cursor'; typed.lastChild.append(' ', cur);
    $('st-spent').textContent = '$3.40'; $('st-steps').textContent = '3/8'; $('st-state').textContent = 'likely $5.10–$10.20 more · on track';
  }
  typeAll();

  /* ---------- entrances ----------
     Reveal every part that is on screen or already scrolled past. Checked on load and on every
     scroll frame, so a fast jump, a nav link or a restored scroll position never leaves content hidden. */
  let pending = [...document.querySelectorAll('.rise')];
  let revealQueued = false;
  function reveal() {
    revealQueued = false;
    const line = innerHeight * 0.94;
    const now = pending.filter((n) => n.getBoundingClientRect().top < line);
    if (!now.length) return;
    pending = pending.filter((n) => !now.includes(n));
    now.forEach((n, i) => {
      n.style.transitionDelay = Math.min(i, 6) * 70 + 'ms'; // stagger within one batch, capped
      n.classList.add('in');
    });
  }
  const queueReveal = () => { if (!revealQueued && pending.length) { revealQueued = true; requestAnimationFrame(reveal); } };
  addEventListener('scroll', queueReveal, { passive: true });
  addEventListener('resize', queueReveal);
  addEventListener('load', queueReveal);
  reveal();

  /* ---------- hero install tabs, copy, GitHub stars ---------- */
  const icmd = $('icmd');
  document.querySelectorAll('.itabs button').forEach((b) => b.addEventListener('click', () => {
    document.querySelectorAll('.itabs button').forEach((x) => x.setAttribute('aria-selected', x === b));
    icmd.textContent = b.dataset.cmd;
    $('icopy').textContent = 'Copy';
  }));
  $('icopy').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(icmd.textContent); $('icopy').textContent = 'Copied'; }
    catch { $('icopy').textContent = 'Select it'; }
  });
  // Real star count from the public GitHub API; nothing is shown if it fails.
  fetch('https://api.github.com/repos/ShadowIzzHerZ/agent-spend-firewall')
    .then((r) => (r.ok ? r.json() : null))
    .then((j) => { if (j && typeof j.stargazers_count === 'number') $('stars').textContent = j.stargazers_count.toLocaleString() + ' stars'; })
    .catch(() => {});

  /* ---------- loop-cost calculator (prices per million tokens) ---------- */
  const usd0 = (v) => '$' + (v >= 100 ? Math.round(v).toLocaleString() : v.toFixed(2));
  function calc() {
    const [pin, pout] = $('k-model').value.split(',').map(Number);
    const ctx = +$('k-ctx').value, rate = +$('k-rate').value, hrs = +$('k-hrs').value;
    const perCall = (ctx * pin + 1000 * pout) / 1e6;
    const calls = rate * 60 * hrs;
    $('o-ctx').textContent = ctx.toLocaleString() + ' tokens';
    $('o-rate').textContent = rate;
    $('o-hrs').textContent = hrs;
    $('r-calls').textContent = calls.toLocaleString() + ' calls';
    for (const [id, v] of [['r-without', perCall * calls], ['r-with', perCall * 4]]) {
      const el = $(id); el.textContent = usd0(v);
      el.classList.add('bump'); setTimeout(() => el.classList.remove('bump'), 160);
    }
  }
  ['k-model', 'k-ctx', 'k-rate', 'k-hrs'].forEach((id) => $(id).addEventListener('input', calc));
  calc();

  /* ---------- setup dialog ---------- */
  let agent = 'claude';
  const cmd = $('cmd');
  function showCmd() {
    const b = Math.max(1, +$('f-budget').value || 5);
    cmd.replaceChildren();
    for (const t of ['pitstop init --budget ' + b, 'pitstop serve &', 'pitstop run -- ' + agent]) {
      const d = document.createElement('div'); d.textContent = t; cmd.append(d);
    }
  }
  document.querySelectorAll('.seg button').forEach((btn) => btn.addEventListener('click', () => {
    document.querySelectorAll('.seg button').forEach((x) => x.setAttribute('aria-pressed', x === btn));
    agent = btn.dataset.a; showCmd();
  }));
  $('f-budget').addEventListener('input', showCmd);
  $('setup').addEventListener('submit', async (e) => {
    e.preventDefault();
    try { await navigator.clipboard.writeText(cmd.innerText); $('ok').textContent = 'Copied. Paste into your terminal.'; }
    catch { $('ok').textContent = 'Select the commands above and copy them.'; }
  });
  showCmd();
})();
