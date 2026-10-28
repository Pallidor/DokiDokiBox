// 各页面共用的格式化与数据连接工具
const GT = {
  esc(s) {
    return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  },

  // 电池数值：整数不带小数，否则保留 1 位；负号用 U+2212
  num(n) {
    if (!Number.isFinite(n)) return '—';
    const a = Math.abs(n);
    const r = Math.round(a * 10) / 10;
    const s = Number.isInteger(r)
      ? r.toLocaleString('en-US')
      : r.toLocaleString('en-US', { minimumFractionDigits: 1, maximumFractionDigits: 1 });
    return (n < 0 && r !== 0 ? '−' : '') + s;
  },

  signed(n) {
    return (n > 0 ? '+' : '') + GT.num(n);
  },

  // 盈亏配色：盈利蓝、亏损绿，0 用默认文字色
  PROFIT: '#7FB8FF',
  LOSS: '#3DD6A3',
  pnlColor(n, zero = '') {
    return n > 0 ? GT.PROFIT : n < 0 ? GT.LOSS : zero;
  },

  pct(r, digits = 1) {
    return Number.isFinite(r) ? (r * 100).toFixed(digits) + '%' : '—';
  },

  hms(ms) {
    if (!Number.isFinite(ms) || ms < 0) return '--:--:--';
    const s = Math.floor(ms / 1000);
    const p = (n) => String(n).padStart(2, '0');
    return `${p(Math.floor(s / 3600))}:${p(Math.floor(s / 60) % 60)}:${p(s % 60)}`;
  },

  clock(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    const p = (n) => String(n).padStart(2, '0');
    return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
  },

  store: {
    get(k, d) { try { return localStorage.getItem('gt.' + k) ?? d; } catch { return d; } },
    set(k, v) { try { localStorage.setItem('gt.' + k, v); } catch {} },
  },

  // 订阅服务端推送：有新盲盒、状态或叠加层设置变化时调用 onChange（已节流），另外每 pollMs 毫秒全量刷新一次
  connect({ onChange, pollMs = 10000 }) {
    let timer = null;
    const schedule = () => {
      if (timer) return;
      timer = setTimeout(() => { timer = null; onChange(); }, 400);
    };
    const es = new EventSource('/api/events');
    for (const ev of ['gift', 'status', 'overlay', 'open']) es.addEventListener(ev, schedule);
    if (pollMs) setInterval(onChange, pollMs);
    return es;
  },
};
