// Pennywise client-side glue. Keep it small.

(function () {
  // Dark mode toggle, persisted in localStorage. Initial value is set inline in
  // base.html before paint to avoid a flash of wrong theme.
  window.PennywiseTheme = {
    set(mode) {
      if (mode === 'dark') {
        document.documentElement.classList.add('dark');
      } else {
        document.documentElement.classList.remove('dark');
      }
      try { localStorage.setItem('pw-theme', mode); } catch (e) { /* ignore */ }
    },
    toggle() {
      const next = document.documentElement.classList.contains('dark') ? 'light' : 'dark';
      this.set(next);
    },
  };

  // Read CSRF token from cookie and forward it as an HTMX header on every request.
  function csrfFromCookie() {
    const m = document.cookie.match(/(?:^|;\s*)pennywise_csrf=([a-f0-9]+)/);
    if (!m) return '';
    // The HMAC token is computed server-side from the cookie value; we send the
    // cookie back as the header and also stash the precomputed token (rendered
    // by the server into a meta tag) when present.
    return m[1];
  }

  document.addEventListener('htmx:configRequest', function (evt) {
    const meta = document.querySelector('meta[name="csrf-token"]');
    if (meta) {
      evt.detail.headers['X-CSRF-Token'] = meta.getAttribute('content');
    }
  });

  // ─── Confirm modal ───────────────────────────────────────────
  // Single styled <dialog> in base.html replaces every browser confirm()
  // call across the app. Three trigger paths feed it:
  //   1. PWConfirm({...}) called directly from page-level scripts.
  //   2. Global submit / click interceptors that pick up [data-confirm].
  //   3. HTMX's `htmx:confirm` event so hx-confirm attributes route here.
  //
  // The Promise resolves true on confirm, false on cancel/escape.
  window.PWConfirm = function (opts) {
    return new Promise(function (resolve) {
      var dlg = document.getElementById('pw-confirm');
      if (!dlg || typeof dlg.showModal !== 'function') {
        // Older browser — fall back so the action isn't silently broken.
        resolve(window.confirm((opts && opts.message) || 'Are you sure?'));
        return;
      }
      var titleEl   = document.getElementById('pw-confirm-title');
      var msgEl     = document.getElementById('pw-confirm-message');
      var okBtn     = document.getElementById('pw-confirm-ok');
      var cancelBtn = document.getElementById('pw-confirm-cancel');

      titleEl.textContent   = opts.title || 'Are you sure?';
      msgEl.textContent     = opts.message || '';
      okBtn.textContent     = opts.confirmText || 'Confirm';
      cancelBtn.textContent = opts.cancelText || 'Cancel';
      okBtn.className       = (opts.danger === false) ? 'btn-primary' : 'btn-danger';

      var settled = false;
      var done = function (yes) {
        if (settled) return;
        settled = true;
        okBtn.removeEventListener('click', onOK);
        cancelBtn.removeEventListener('click', onCancel);
        dlg.removeEventListener('close', onClose);
        if (dlg.open) dlg.close();
        resolve(yes);
      };
      var onOK     = function () { done(true);  };
      var onCancel = function () { done(false); };
      var onClose  = function () { done(false); }; // ESC key etc.

      okBtn.addEventListener('click', onOK);
      cancelBtn.addEventListener('click', onCancel);
      dlg.addEventListener('close', onClose);

      dlg.showModal();
      // Focus Cancel by default — safer for destructive actions where the
      // user might hit Enter reflexively.
      cancelBtn.focus();
    });
  };

  // Forms: any <form data-confirm="…"> or any submit button inside a form
  // that has [data-confirm] gets intercepted. The form re-submits naturally
  // once the user confirms (we mark a one-shot flag so we don't re-prompt).
  document.addEventListener('submit', function (evt) {
    var form = evt.target;
    if (!form || form.tagName !== 'FORM') return;
    if (form.dataset.pwConfirmed === 'yes') return; // resubmit after confirm
    var submitter = evt.submitter;
    var msg = (submitter && submitter.dataset.confirm) || form.dataset.confirm;
    if (!msg) return;
    evt.preventDefault();
    PWConfirm({ message: msg, danger: true }).then(function (yes) {
      if (!yes) return;
      form.dataset.pwConfirmed = 'yes';
      // Click the original submitter so the form posts with the same
      // name=value that triggered the original submit (matters for forms
      // with multiple submit buttons, e.g. expense edit's Save / Delete).
      if (submitter && typeof submitter.click === 'function') {
        submitter.click();
      } else {
        form.submit();
      }
      delete form.dataset.pwConfirmed;
    });
  }, true);

  // Plain links: <a data-confirm="…" href="…">
  document.addEventListener('click', function (evt) {
    var a = evt.target.closest && evt.target.closest('a[data-confirm]');
    if (!a) return;
    if (a.dataset.pwConfirmed === 'yes') return;
    evt.preventDefault();
    PWConfirm({ message: a.getAttribute('data-confirm'), danger: true }).then(function (yes) {
      if (!yes) return;
      a.dataset.pwConfirmed = 'yes';
      window.location.href = a.href;
    });
  }, true);

  // HTMX: route hx-confirm through PWConfirm too.
  document.body && document.body.addEventListener('htmx:confirm', function (evt) {
    if (!evt.detail.question) return;
    evt.preventDefault();
    PWConfirm({ message: evt.detail.question, danger: true }).then(function (yes) {
      if (yes) evt.detail.issueRequest(true);
    });
  });

  // ─── Click-to-copy ────────────────────────────────────────────
  // Any element with [data-copy] becomes click-to-copy. The text copied is
  // the attribute value if non-empty, otherwise the element's textContent.
  // After a successful copy the element gets the .is-copied class for a
  // moment so CSS can flash a confirmation without us juggling DOM.
  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    // Fallback for non-secure contexts (file://, http on a remote host).
    return new Promise(function (resolve, reject) {
      try {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        resolve();
      } catch (e) {
        reject(e);
      }
    });
  }
  document.addEventListener('click', function (evt) {
    var el = evt.target.closest('[data-copy]');
    if (!el) return;
    evt.preventDefault();
    var text = el.getAttribute('data-copy');
    if (!text) text = (el.textContent || '').trim();
    if (!text) return;
    copyText(text).then(function () {
      el.classList.add('is-copied');
      setTimeout(function () { el.classList.remove('is-copied'); }, 1400);
    });
  });

  // ─── Combobox (searchable select) ────────────────────────────
  // Pattern: a button-styled trigger opens a panel containing a search input
  // and a scrollable list. Keyboard fully supported (Up/Down/Enter/Esc).
  // The actual form value lives in a hidden <input>; selecting an option
  // sets its value and dispatches a `change` event so page-level scripts
  // (e.g. the symbol auto-fill) can react.
  function initCombobox(root) {
    var trigger = root.querySelector('[data-cb-trigger]');
    var panel   = root.querySelector('[data-cb-panel]');
    var search  = root.querySelector('[data-cb-search]');
    var list    = root.querySelector('[data-cb-list]');
    var hidden  = root.querySelector('[data-cb-value]');
    var display = root.querySelector('[data-cb-display]');
    var empty   = root.querySelector('[data-cb-empty]');
    if (!trigger || !panel || !search || !list || !hidden || !display) return;
    var options = Array.prototype.slice.call(list.querySelectorAll('.combobox-option'));
    var placeholder = display.dataset.placeholder || 'Select…';

    function isOpen() { return !panel.hidden; }

    function open() {
      panel.hidden = false;
      trigger.setAttribute('aria-expanded', 'true');
      search.value = '';
      filter('');
      // Scroll the currently-selected option into view if any.
      var sel = list.querySelector('.combobox-option[data-selected="true"]');
      if (sel) sel.scrollIntoView({ block: 'nearest' });
      // Defer focus so the click that opened the panel doesn't immediately blur it.
      setTimeout(function () { search.focus(); }, 0);
    }

    function close() {
      panel.hidden = true;
      trigger.setAttribute('aria-expanded', 'false');
    }

    function filter(q) {
      q = foldKey(q);
      var visible = 0;
      var firstVisible = null;
      options.forEach(function (opt) {
        var match = !q || opt.dataset.search.indexOf(q) !== -1;
        opt.hidden = !match;
        opt.classList.remove('is-active');
        if (match) {
          visible++;
          if (!firstVisible) firstVisible = opt;
        }
      });
      if (empty) empty.hidden = visible > 0;
      if (firstVisible) firstVisible.classList.add('is-active');
    }

    function visibleOptions() {
      return options.filter(function (o) { return !o.hidden; });
    }

    function activeIndex() {
      var v = visibleOptions();
      for (var i = 0; i < v.length; i++) {
        if (v[i].classList.contains('is-active')) return i;
      }
      return -1;
    }

    function setActive(idx) {
      var v = visibleOptions();
      if (v.length === 0) return;
      if (idx < 0) idx = v.length - 1;
      if (idx >= v.length) idx = 0;
      options.forEach(function (o) { o.classList.remove('is-active'); });
      v[idx].classList.add('is-active');
      v[idx].scrollIntoView({ block: 'nearest' });
    }

    function select(opt) {
      hidden.value = opt.dataset.value;
      hidden.dataset.symbol = opt.dataset.symbol || '';
      display.textContent = opt.dataset.label || opt.textContent.trim();
      display.classList.remove('is-placeholder');
      options.forEach(function (o) { o.removeAttribute('data-selected'); });
      opt.setAttribute('data-selected', 'true');
      hidden.dispatchEvent(new Event('change', { bubbles: true }));
      close();
      trigger.focus();
    }

    // Initial display: read from selected option, or fall back to placeholder.
    (function paintInitial() {
      var preselected = list.querySelector('.combobox-option[data-selected="true"]');
      if (!preselected && hidden.value) {
        for (var i = 0; i < options.length; i++) {
          if (options[i].dataset.value === hidden.value) {
            preselected = options[i];
            preselected.setAttribute('data-selected', 'true');
            break;
          }
        }
      }
      if (preselected) {
        display.textContent = preselected.dataset.label || preselected.textContent.trim();
        display.classList.remove('is-placeholder');
        hidden.dataset.symbol = preselected.dataset.symbol || '';
      } else {
        display.textContent = placeholder;
        display.classList.add('is-placeholder');
      }
    })();

    trigger.addEventListener('click', function () { isOpen() ? close() : open(); });
    trigger.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        if (!isOpen()) open();
      }
    });

    search.addEventListener('input', function () { filter(search.value); });
    search.addEventListener('keydown', function (e) {
      switch (e.key) {
        case 'ArrowDown': e.preventDefault(); setActive(activeIndex() + 1); break;
        case 'ArrowUp':   e.preventDefault(); setActive(activeIndex() - 1); break;
        case 'Enter': {
          e.preventDefault();
          var v = visibleOptions();
          var i = activeIndex();
          if (v.length && i >= 0) select(v[i]);
          break;
        }
        case 'Escape': e.preventDefault(); close(); trigger.focus(); break;
      }
    });

    options.forEach(function (opt) {
      opt.addEventListener('click', function (e) {
        e.preventDefault();
        select(opt);
      });
      opt.addEventListener('mouseenter', function () {
        options.forEach(function (o) { o.classList.remove('is-active'); });
        opt.classList.add('is-active');
      });
    });

    document.addEventListener('mousedown', function (e) {
      if (isOpen() && !root.contains(e.target)) close();
    });
  }

  // Same Latin-accent fold as internal/models/currency.go's foldKey, so
  // typing "cordoba" matches "Córdoba" client-side too.
  function foldKey(s) {
    if (!s) return '';
    return s.toLowerCase()
      .replace(/[áàâãäåăā]/g, 'a')
      .replace(/[éèêëē]/g,    'e')
      .replace(/[íìîïī]/g,    'i')
      .replace(/[óòôõöøō]/g,  'o')
      .replace(/[úùûüū]/g,    'u')
      .replace(/[ñ]/g,        'n')
      .replace(/[ç]/g,        'c')
      .replace(/[ł]/g,        'l')
      .replace(/[đðĐ]/g,      'd')
      .replace(/[żź]/g,       'z')
      .trim();
  }

  document.addEventListener('DOMContentLoaded', function () {
    document.querySelectorAll('[data-combobox]').forEach(initCombobox);
  });
})();
