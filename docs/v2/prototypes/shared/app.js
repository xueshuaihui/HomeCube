/* 家立方 · 原型运行时（主题、热区、状态切换、提示）
 * 只服务原型，不是产品代码。 */
(function () {
  'use strict';

  var THEME_KEY = 'hc-proto-theme';
  var params = new URLSearchParams(location.search);
  var state = params.get('state');

  /* ---------------- 主题 ---------------- */
  var mql = window.matchMedia('(prefers-color-scheme: dark)');

  function preference() {
    var forced = params.get('theme');
    if (forced === 'light' || forced === 'dark') return forced;
    return localStorage.getItem(THEME_KEY) || 'system';
  }

  function resolved() {
    var p = preference();
    if (p === 'system') return mql.matches ? 'dark' : 'light';
    return p;
  }

  function applyTheme() {
    document.documentElement.setAttribute('data-theme', resolved());
    var box = document.querySelector('[data-theme-switch]');
    if (box) {
      box.querySelectorAll('button').forEach(function (b) {
        b.setAttribute('aria-pressed', String(b.dataset.themeValue === preference()));
      });
    }
  }

  function setTheme(v) {
    if (v === 'system') localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, v);
    applyTheme();
  }

  mql.addEventListener('change', function () {
    if (preference() === 'system') applyTheme();
  });

  /* ---------------- 提示与弹层 ---------------- */
  var toastTimer = null;

  function toast(msg) {
    var el = document.querySelector('.toast');
    if (!el) {
      el = document.createElement('div');
      el.className = 'toast';
      (document.querySelector('.phone') || document.body).appendChild(el);
    }
    el.textContent = msg;
    el.classList.add('is-show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { el.classList.remove('is-show'); }, 2000);
  }

  function openSheet(id) {
    var el = document.getElementById(id);
    if (!el) return;
    el.classList.add('is-open');
    el.setAttribute('aria-hidden', 'false');
  }

  function closeSheet(el) {
    el.classList.remove('is-open');
    el.setAttribute('aria-hidden', 'true');
  }

  /* ---------------- 热区标注 ---------------- */
  var POS = {
    'top-right': { top: '6px', right: '6px' },
    'top-left': { top: '6px', left: '6px' },
    'center': { top: '50%', left: '50%', transform: 'translate(-50%,-50%)' },
    'bottom-right': { bottom: '6px', right: '6px' },
    'bottom-left': { bottom: '6px', left: '6px' },
    'outer-top-right': { top: '-11px', right: '-11px' },
    'outer-top-left': { top: '-11px', left: '-11px' },
    'outer-bottom-right': { bottom: '-11px', right: '-11px' }
  };

  var CIRCLED = '①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳';

  function hotIndex(el) {
    var i = CIRCLED.indexOf(el.getAttribute('data-hot') || '');
    return i < 0 ? 99 : i;
  }

  function buildHotspots() {
    var hosts = Array.prototype.slice.call(document.querySelectorAll('[data-hot]'));
    if (!hosts.length) return;
    var panel = document.createElement('aside');
    panel.className = 'hotpanel';
    panel.innerHTML = '<h4>交互标注</h4><ol></ol>';
    var ol = panel.querySelector('ol');

    hosts.sort(function (a, b) { return hotIndex(a) - hotIndex(b); });

    hosts.forEach(function (host) {
      host.classList.add('hot-host');
      var pin = document.createElement('span');
      pin.className = 'hot';
      pin.textContent = host.getAttribute('data-hot');
      var pos = POS[host.getAttribute('data-hot-pos') || 'top-right'];
      Object.keys(pos).forEach(function (k) { pin.style[k] = pos[k]; });
      host.appendChild(pin);

      var li = document.createElement('li');
      li.innerHTML = '<b>' + host.getAttribute('data-hot') + ' ' +
        escapeHtml(host.getAttribute('data-hot-name') || '') + '</b>' +
        (host.getAttribute('data-hot-label') ? ' — ' + escapeHtml(host.getAttribute('data-hot-label')) : '');
      ol.appendChild(li);
    });

    document.body.appendChild(panel);
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  function toggleAnnotate(on) {
    document.body.classList.toggle('annotate', on);
    var btn = document.querySelector('[data-annotate-toggle]');
    if (btn) btn.setAttribute('aria-pressed', String(on));
  }

  /* ---------------- 状态切换 ---------------- */
  function applyState() {
    document.body.setAttribute('data-state', state);
    document.querySelectorAll('[data-state-block]').forEach(function (el) {
      el.hidden = el.getAttribute('data-state-block') !== state;
    });
    var sel = document.querySelector('[data-state-select]');
    if (sel) sel.value = state;
  }

  /* ---------------- 原型工具层 ---------------- */
  function buildHarness() {
    var host = document.querySelector('.harness');
    if (!host) return;
    var title = document.body.getAttribute('data-screen') || document.title;
    var states = (document.body.getAttribute('data-states') || '').split(',').filter(Boolean);
    var opts = states.map(function (s) {
      return '<option value="' + s + '">' + s + '</option>';
    }).join('');

    host.innerHTML =
      '<div class="harness-card">' +
        '<h4>' + escapeHtml(title) + '</h4>' +
        '<div class="harness-row" data-theme-switch>' +
          '<button data-theme-value="system">跟随系统</button>' +
          '<button data-theme-value="light">亮</button>' +
          '<button data-theme-value="dark">暗</button>' +
        '</div>' +
        '<div class="harness-row" style="margin-top:8px">' +
          '<button data-annotate-toggle aria-pressed="false">交互标注</button>' +
          '<a href="index.html" style="align-self:center;opacity:.7">返回页面墙</a>' +
        '</div>' +
      '</div>' +
      (states.length ?
        '<div class="harness-card"><h4>页面状态</h4>' +
          '<select data-state-select style="width:100%;padding:6px;border-radius:8px;border:1px solid var(--harness-border);background:transparent;color:inherit;font-family:inherit;font-size:12px">' +
            opts +
          '</select></div>' : '');

    host.addEventListener('click', function (e) {
      var t = e.target.closest('button');
      if (!t) return;
      if (t.dataset.themeValue) setTheme(t.dataset.themeValue);
      if (t.hasAttribute('data-annotate-toggle')) toggleAnnotate(!document.body.classList.contains('annotate'));
    });
    host.addEventListener('change', function (e) {
      if (e.target.matches('[data-state-select]')) {
        var u = new URL(location.href);
        if (e.target.value === 'default') u.searchParams.delete('state');
        else u.searchParams.set('state', e.target.value);
        location.href = u.toString();
      }
    });
  }

  /* ---------------- 全局事件 ---------------- */
  document.addEventListener('click', function (e) {
    var themeBtn = e.target.closest('[data-theme-value]');
    if (themeBtn) setTheme(themeBtn.getAttribute('data-theme-value'));

    var go = e.target.closest('[data-go]');
    if (go) { location.href = go.getAttribute('data-go'); return; }

    var unbuilt = e.target.closest('[data-unbuilt]');
    if (unbuilt) {
      e.preventDefault();
      toast('该页面在' + (unbuilt.getAttribute('data-unbuilt') || '后续批次') + '交付');
      return;
    }

    var open = e.target.closest('[data-sheet-open]');
    if (open) { openSheet(open.getAttribute('data-sheet-open')); return; }

    var close = e.target.closest('[data-sheet-close]');
    if (close) { closeSheet(close.closest('.sheet-mask')); return; }

    var mask = e.target.closest('.sheet-mask');
    if (mask && e.target === mask) closeSheet(mask);

    var cd = e.target.closest('[data-countdown]');
    if (cd && !cd.disabled) startCountdown(cd);

    var toastBtn = e.target.closest('[data-toast]');
    if (toastBtn) { e.preventDefault(); toast(toastBtn.getAttribute('data-toast')); }
  });

  function startCountdown(btn) {
    var left = Number(btn.getAttribute('data-countdown')) || 60;
    var text = btn.textContent;
    btn.disabled = true;
    btn.textContent = left + 's';
    var timer = setInterval(function () {
      left -= 1;
      if (left <= 0) {
        clearInterval(timer);
        btn.disabled = false;
        btn.textContent = text;
      } else {
        btn.textContent = left + 's';
      }
    }, 1000);
  }

  /* ---------------- 色值显示（主题页用） ---------------- */
  function colorOf(token, theme) {
    var probe = document.createElement('span');
    probe.style.cssText = 'position:absolute;left:-9999px;color:var(' + token + ')';
    if (theme) probe.setAttribute('data-theme', theme);
    document.body.appendChild(probe);
    var c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  }

  function toHex(c) {
    var m = c.match(/rgba?\(([^)]+)\)/);
    if (!m) return '';
    return '#' + m[1].split(',').slice(0, 3).map(function (n) {
      var h = Math.round(parseFloat(n)).toString(16).toUpperCase();
      return h.length === 1 ? '0' + h : h;
    }).join('');
  }

  function fillHex() {
    document.querySelectorAll('[data-hex]').forEach(function (el) {
      var token = el.getAttribute('data-hex');
      var light = toHex(colorOf(token, 'light'));
      el.textContent = el.hasAttribute('data-hex-both')
        ? '亮 ' + light + ' / 暗 ' + toHex(colorOf(token, 'dark'))
        : light;
    });
  }

  /* ---------------- 启动 ---------------- */
  document.addEventListener('DOMContentLoaded', function () {
    applyTheme();
    fillHex();
    if (!state) {
      var declared = (document.body.getAttribute('data-states') || '').split(',')[0];
      state = declared || 'default';
    }
    var embedded = params.get('embed') === '1';
    if (!embedded) {
      buildHarness();
      buildHotspots();
    }
    applyState();
    if (params.get('annotate') === '1') toggleAnnotate(true);
    document.querySelectorAll('[data-tabbar]').forEach(function (bar) {
      var name = document.body.getAttribute('data-tab');
      bar.querySelectorAll('.tab').forEach(function (t) {
        if (t.getAttribute('data-tab') === name) t.setAttribute('aria-current', 'page');
      });
    });
  });

  window.HC = {
    toast: toast,
    openSheet: openSheet,
    closeSheet: closeSheet,
    setTheme: setTheme,
    get state() { return state; },
    money: function (cents) {
      var sign = cents < 0 ? '-' : '';
      return sign + '¥' + (Math.abs(cents) / 100).toFixed(2);
    }
  };
})();
