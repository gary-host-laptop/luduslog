/* LudusLog progressive enhancement. The site works fully without this file. */
(function () {
  "use strict";

  function cookie(name) {
    var parts = document.cookie ? document.cookie.split("; ") : [];
    for (var i = 0; i < parts.length; i++) {
      var kv = parts[i].split("=");
      if (kv[0] === name) return decodeURIComponent(kv.slice(1).join("="));
    }
    return "";
  }

  // htmx sends its own requests; attach the CSRF token and ask for fragments.
  document.body.addEventListener("htmx:configRequest", function (evt) {
    var token = cookie("luduslog_csrf");
    if (token) evt.detail.headers["X-CSRF-Token"] = token;
  });

  // Keep htmx history entries from breaking scroll restoration.
  document.body.addEventListener("htmx:afterSwap", function () {
    document.querySelectorAll("[data-autofocus]").forEach(function (el) { el.focus(); });
  });

  // Theme toggle: system → light → dark → system.
  var key = "luduslog-theme";
  function applyTheme(mode) {
    var root = document.documentElement;
    if (mode === "light" || mode === "dark") {
      root.setAttribute("data-theme", mode);
    } else {
      root.removeAttribute("data-theme");
    }
    var btn = document.getElementById("theme-toggle");
    if (btn) btn.setAttribute("aria-label", "Theme: " + (mode || "system"));
  }
  function currentTheme() {
    try { return localStorage.getItem(key) || "system"; } catch (e) { return "system"; }
  }
  applyTheme(currentTheme());

  var actions = document.querySelector(".header-actions");
  if (actions && !document.getElementById("theme-toggle")) {
    var btn = document.createElement("button");
    btn.id = "theme-toggle";
    btn.type = "button";
    btn.className = "icon-btn";
    btn.textContent = "◐";
    btn.title = "Change theme";
    btn.addEventListener("click", function () {
      var order = ["system", "light", "dark"];
      var next = order[(order.indexOf(currentTheme()) + 1) % order.length];
      try { localStorage.setItem(key, next); } catch (e) {}
      applyTheme(next);
    });
    actions.insertBefore(btn, actions.firstChild);
  }

  // Copy buttons: elements with data-copy copy their value and confirm briefly.
  document.addEventListener("click", function (evt) {
    var el = evt.target.closest("[data-copy]");
    if (!el) return;
    var text = el.getAttribute("data-copy");
    if (!navigator.clipboard) return;
    navigator.clipboard.writeText(text).then(function () {
      var old = el.textContent;
      el.textContent = "Copied!";
      setTimeout(function () { el.textContent = old; }, 1500);
    });
  });

  // Spoiler toggles.
  document.addEventListener("click", function (evt) {
    var btn = evt.target.closest("[data-spoiler-toggle]");
    if (!btn) return;
    var target = document.getElementById(btn.getAttribute("data-spoiler-toggle"));
    if (target) target.hidden = !target.hidden;
  });
})();
