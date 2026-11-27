/* WoPan storage form helper.
 *
 * Injects a "发送短信验证码" button next to the sms_code field of the WoPan storage
 * form. It exists because OpenList re-initialises a storage on every load, so an
 * SMS request triggered by saving the form would send a new message each time.
 * With this button, one click = one message.
 *
 * The snippet is served from /public/wopan_form.js and pulled in through the
 * "customize body" setting, so it survives frontend rebuilds (the dist bundle is
 * upstream and re-downloaded by CI on every build).
 */
(function () {
  'use strict';

  var DRIVER = 'WoPan';
  var LABEL = '发送短信验证码';
  var API = '/api/admin/storage/sms_code';
  var MARK = 'data-wopan-sms-button';

  function apiBase() {
    // The frontend may be served under a base path; window.__os_routes or the
    // <base> tag are not reliable, so derive it from the current pathname.
    var m = window.location.pathname.match(/^(.*?)\/@manage\//);
    return m ? m[1] : '';
  }

  function storageId() {
    var m = window.location.pathname.match(/\/storages\/edit\/(\d+)/);
    return m ? m[1] : null;
  }

  function token() {
    // OpenList stores the JWT in localStorage under "token".
    return window.localStorage.getItem('token') || '';
  }

  function findFieldByLabel(names) {
    // The form renders a <label class="hope-form-label"> whose text is the field
    // name prettified ("sms_code" -> "Sms code"), so match case-insensitively on
    // any of the accepted spellings.
    var labels = document.querySelectorAll('label');
    for (var i = 0; i < labels.length; i++) {
      var el = labels[i];
      var t = el.textContent.trim().toLowerCase();
      for (var j = 0; j < names.length; j++) {
        if (t === names[j].toLowerCase()) {
          return el;
        }
      }
    }
    return null;
  }

  function flash(btn, message, ok) {
    var old = btn.textContent;
    btn.textContent = message;
    btn.disabled = true;
    btn.style.opacity = '0.75';
    setTimeout(function () {
      btn.textContent = old;
      btn.disabled = false;
      btn.style.opacity = '';
    }, ok ? 4000 : 6000);
  }

  function send(btn, id) {
    btn.disabled = true;
    btn.textContent = '发送中…';
    fetch(apiBase() + API + '?id=' + encodeURIComponent(id), {
      method: 'POST',
      headers: { 'Authorization': token(), 'Content-Type': 'application/json' }
    })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, body: j }; }); })
      .then(function (res) {
        if (res.ok && res.body && res.body.code === 200) {
          flash(btn, '已发送，请查收短信', true);
        } else {
          var msg = (res.body && (res.body.message || res.body.data)) || '发送失败';
          flash(btn, String(msg).slice(0, 40), false);
          console.error('[wopan] send sms failed', res.body);
        }
      })
      .catch(function (err) {
        flash(btn, '请求失败，看控制台', false);
        console.error('[wopan] send sms error', err);
      });
  }

  function inject() {
    var id = storageId();
    if (!id) return;
    if (document.querySelector('[' + MARK + ']')) return;

    // Only for the WoPan storage: the form shows the driver name in its header.
    var pageText = document.body.textContent || '';
    if (pageText.indexOf(DRIVER) === -1 && pageText.indexOf('联通云盘') === -1) return;

    var label = findFieldByLabel(['sms_code', 'sms code', '短信验证码']);
    if (!label) return;

    // Attach right after the label so it sits with the input it feeds.
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.setAttribute(MARK, '1');
    btn.textContent = LABEL;
    btn.title = '每点一次只发一条短信验证码；保存表单不会自动发送';
    btn.style.cssText = [
      'margin-left:8px', 'padding:2px 10px', 'border-radius:6px',
      'border:1px solid currentColor', 'background:transparent',
      'color:inherit', 'cursor:pointer', 'font-size:12px', 'vertical-align:middle'
    ].join(';');
    btn.addEventListener('click', function (ev) {
      ev.preventDefault();
      send(btn, id);
    });
    label.appendChild(btn);
  }

  // The admin page is a SPA: watch for navigation and for the form to render.
  var pending = null;
  function schedule() {
    if (pending) return;
    pending = setTimeout(function () {
      pending = null;
      try { inject(); } catch (e) { console.error('[wopan] inject failed', e); }
    }, 300);
  }

  var observer = new MutationObserver(schedule);
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('popstate', schedule);
  window.addEventListener('hashchange', schedule);
  document.addEventListener('DOMContentLoaded', schedule);
  schedule();
})();
