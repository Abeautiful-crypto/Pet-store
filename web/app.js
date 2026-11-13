/* 毛球驿站 · 高保真原型 API 联调层
   前端只做展示与格式化：金额一律为后端「分」，formatYuan 不参与任何算术比较。
   每个写操作第一行过 requireRole 守卫；隐藏按钮不等于安全，后端仍独立校验。 */
(function () {
  'use strict';

  var ROLE_LABELS = {
    user: '宠物主人', frontdesk: '前台及服务人员', cs: '客服',
    manager: '店长', channel: '模拟支付渠道'
  };
  var ALL_ROLES = ['user', 'frontdesk', 'cs', 'manager', 'channel'];
  var RULES = {
    refresh: ALL_ROLES, query: ALL_ROLES,
    publishSlot: ['manager'], createAppointment: ['user'], pay: ['user', 'channel'],
    cancel: ['user'], review: ['cs'], retryRefund: ['cs'],
    verify: ['frontdesk'], startService: ['frontdesk'], finishService: ['frontdesk'],
    noShow: ['manager'], expireHolds: ['manager', 'channel'],
    channelRefundFail: ['channel'], channelRefundOk: ['channel']
  };

  var state = {
    role: 'manager', user: 'U1', store: 'STORE-1',
    appointment: '', request: '', notify: 0
  };

  function requireRole(role, allowed, action) {
    if (!allowed || allowed.indexOf(role) === -1) {
      throw new Error('无权访问：' + action + '（本次操作被拦截，未做任何数据改动）');
    }
    return true;
  }

  function formatYuan(fen) {
    return '\u00A5' + (Number(fen) / 100).toFixed(2);
  }

  function el(tag, cls, text) {
    var node = document.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function val(id) {
    var node = document.getElementById(id);
    return node ? node.value.trim() : '';
  }

  function toast(message, bad) {
    var box = document.getElementById('ps-toast');
    box.textContent = message;
    box.style.background = bad ? '#E5484D' : '#0E9F6E';
    box.style.display = 'block';
    clearTimeout(toast.timer);
    toast.timer = setTimeout(function () { box.style.display = 'none'; }, 3600);
  }

  function renderOut(data) {
    document.getElementById('ps-output').textContent = JSON.stringify(data, null, 2);
  }

  async function api(method, path, body, action) {
    requireRole(state.role, RULES[action], action);
    var response = await fetch(path, {
      method: method,
      headers: {
        'Content-Type': 'application/json',
        'X-Role': state.role,
        'X-User-Id': state.user,
        'X-Store-Id': state.store
      },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    var data = await response.json().catch(function () { return {}; });
    if (!response.ok) throw new Error(data.error || ('HTTP ' + response.status));
    return data;
  }

  function apptPath(suffix) {
    var id = val('ps-appt') || state.appointment;
    if (!id) throw new Error('请先填写预约号（创建预约会自动带入）');
    return '/api/appointments/' + encodeURIComponent(id) + suffix;
  }

  function slotRange() {
    var start = new Date(Date.now() + 4 * 3600 * 1000);
    var end = new Date(start.getTime() + 3600 * 1000);
    return { start_at: start.toISOString(), end_at: end.toISOString() };
  }

  var ACTIONS = [
    ['refresh', '刷新时段与日清', function () { return api('GET', '/api/slots', undefined, 'refresh'); }],
    ['query', '查询预约四种结局', function () { return api('GET', apptPath(''), undefined, 'query'); }],
    ['publishSlot', '发布时段', function () {
      var range = slotRange();
      return api('POST', '/api/slots', { slot_id: val('ps-slot'), start_at: range.start_at, end_at: range.end_at }, 'publishSlot');
    }],
    ['createAppointment', '创建预约', function () {
      return api('POST', '/api/appointments', { pet_id: val('ps-pet'), slot_id: val('ps-slot') }, 'createAppointment');
    }],
    ['pay', '支付 / 重复通知', function () {
      state.notify += 1;
      return api('POST', apptPath('/pay'), { notify_key: 'WEB-N' + state.notify, channel_trade_no: 'WEB-T' + state.notify }, 'pay');
    }],
    ['cancel', '取消预约', function () {
      return api('POST', apptPath('/cancel'), { reason: val('ps-reason') }, 'cancel');
    }],
    ['review', '客服批准取消', function () {
      return api('POST', '/api/cancel-requests/' + encodeURIComponent(val('ps-request') || state.request) + '/review', { approve: true }, 'review');
    }],
    ['retryRefund', '退款失败重试', function () {
      return api('POST', '/api/refunds/retry', { appointment_id: val('ps-appt') || state.appointment }, 'retryRefund');
    }],
    ['verify', '前台核销', function () { return api('POST', apptPath('/verify'), {}, 'verify'); }],
    ['startService', '登记服务开始', function () { return api('POST', apptPath('/start'), {}, 'startService'); }],
    ['finishService', '登记服务完成', function () { return api('POST', apptPath('/finish'), {}, 'finishService'); }],
    ['noShow', '标记未到店', function () {
      return api('POST', apptPath('/no-show'), { reason: val('ps-reason') || '主人未到场' }, 'noShow');
    }],
    ['expireHolds', '扫描 5 分钟超时', function () { return api('POST', '/api/holds/expire', {}, 'expireHolds'); }],
    ['channelRefundFail', '渠道：退款失败', function () {
      return api('POST', '/api/channel/refund-outcome', { succeeds: false }, 'channelRefundFail');
    }],
    ['channelRefundOk', '渠道：退款成功', function () {
      return api('POST', '/api/channel/refund-outcome', { succeeds: true }, 'channelRefundOk');
    }]
  ];

  async function refresh() {
    try {
      var slots = await api('GET', '/api/slots', undefined, 'refresh');
      var box = document.getElementById('ps-slots');
      box.textContent = '';
      (slots.slots || []).forEach(function (slot) {
        box.appendChild(el('div', 'ps-row',
          slot.ID + ' · ' + new Date(slot.StartAt).toLocaleString('zh-CN') +
          ' · 名额 ' + slot.OccupiedCount + '/' + slot.Capacity));
      });
      var metrics = document.getElementById('ps-metrics');
      if (state.role === 'manager') {
        var st = await api('GET', '/api/settlement', undefined, 'refresh');
        metrics.textContent = '已收款 ' + formatYuan(st.CollectedFen) +
          ' · 已退款 ' + formatYuan(st.RefundedFen) +
          ' · 未退款 ' + formatYuan(st.UnrefundedFen) +
          ' · 已完成服务收入 ' + formatYuan(st.RevenueFen);
      } else {
        metrics.textContent = '当前角色：' + ROLE_LABELS[state.role] + '（日清仅店长可见）';
      }
    } catch (e) {
      toast(String(e.message || e), true);
    }
  }

  async function run(action, handler, label) {
    try {
      var out = await handler();
      if (out && out.ID) {
        state.appointment = out.ID;
        document.getElementById('ps-appt').value = out.ID;
      }
      if (out && out.CancelRequest && out.CancelRequest.ID) {
        state.request = out.CancelRequest.ID;
        document.getElementById('ps-request').value = out.CancelRequest.ID;
      }
      renderOut(out);
      toast(label + ' 成功');
      await refresh();
    } catch (e) {
      toast(String(e.message || e), true);
    }
  }

  function buildPanel() {
    var style = document.createElement('style');
    style.textContent = '#ps-panel{position:fixed;right:18px;bottom:18px;width:390px;max-height:78vh;overflow:auto;z-index:9999;background:#fff;border:1px solid #E6EBF4;border-radius:16px;box-shadow:0 22px 55px rgba(26,36,68,.18);padding:16px;font-size:12.5px;color:#1A2444}' +
      '#ps-panel h3{margin:0 0 10px;font-size:15px}#ps-panel .ps-grid{display:grid;grid-template-columns:1fr 1fr;gap:6px;margin-bottom:8px}' +
      '#ps-panel input,#ps-panel select{width:100%;border:1px solid #E6EBF4;border-radius:8px;padding:6px 8px;font:inherit}' +
      '#ps-panel button{border:0;border-radius:8px;background:#FF6A3D;color:#fff;padding:6px 8px;cursor:pointer;font-size:12px;margin:2px}' +
      '#ps-panel button.ps-sec{background:#4C5BD4}#ps-metrics{background:#F2F5FB;border-radius:8px;padding:8px;margin:8px 0;font-weight:600}' +
      '.ps-row{border-bottom:1px solid #EFF2F8;padding:3px 0}#ps-output{background:#16204A;color:#DCE4FF;border-radius:8px;padding:8px;max-height:150px;overflow:auto;font-size:11px}' +
      '#ps-toast{display:none;color:#fff;border-radius:8px;padding:8px;margin-bottom:8px}#ps-toggle{position:fixed;right:18px;bottom:18px;z-index:9998;border:0;border-radius:999px;background:#16204A;color:#fff;padding:10px 16px;cursor:pointer;box-shadow:0 10px 30px rgba(26,36,68,.25)}';
    document.head.appendChild(style);

    var toggle = el('button', '', '实时联调');
    toggle.id = 'ps-toggle';
    document.body.appendChild(toggle);

    var panel = el('div', '');
    panel.id = 'ps-panel';
    panel.style.display = 'none';
    panel.appendChild(el('h3', '', '毛球驿站 · 实时 API 联调'));
    panel.appendChild(el('div', '', '写操作均经 requireRole 守卫；金额由后端「分」格式化展示'));

    var toastBox = el('div', '');
    toastBox.id = 'ps-toast';
    panel.appendChild(toastBox);

    var grid = el('div', 'ps-grid');
    var roleSelect = el('select', '');
    ALL_ROLES.forEach(function (role) {
      var opt = el('option', '', ROLE_LABELS[role]);
      opt.value = role;
      roleSelect.appendChild(opt);
    });
    roleSelect.id = 'ps-role';
    roleSelect.value = state.role;
    roleSelect.addEventListener('change', function () {
      state.role = roleSelect.value;
      refresh();
    });
    grid.appendChild(roleSelect);

    [['ps-user', state.user], ['ps-store', state.store], ['ps-slot', 'SL1'],
      ['ps-pet', 'P1'], ['ps-appt', ''], ['ps-reason', ''], ['ps-request', '']].forEach(function (item) {
      var input = el('input', '');
      input.id = item[0];
      input.value = item[1];
      input.placeholder = item[0].replace('ps-', '');
      grid.appendChild(input);
    });
    panel.appendChild(grid);

    var buttons = el('div', '');
    ACTIONS.forEach(function (item) {
      var btn = el('button', item[0] === 'refresh' || item[0] === 'query' ? 'ps-sec' : '', item[1]);
      btn.addEventListener('click', function () { run(item[0], item[2], item[1]); });
      buttons.appendChild(btn);
    });
    panel.appendChild(buttons);

    var metrics = el('div', '');
    metrics.id = 'ps-metrics';
    panel.appendChild(metrics);
    panel.appendChild(el('div', '', '时段列表'));
    var slotsBox = el('div', '');
    slotsBox.id = 'ps-slots';
    panel.appendChild(slotsBox);
    var out = el('pre', '');
    out.id = 'ps-output';
    panel.appendChild(out);
    document.body.appendChild(panel);

    toggle.addEventListener('click', function () {
      var show = panel.style.display === 'none';
      panel.style.display = show ? 'block' : 'none';
      toggle.style.bottom = show ? 'calc(78vh + 26px)' : '18px';
      if (show) refresh();
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { buildPanel(); refresh(); });
  } else {
    buildPanel();
    refresh();
  }
})();
