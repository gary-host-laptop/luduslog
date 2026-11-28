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

  // Quick rating: choosing a half star saves through the existing form. The
  // noscript Save button remains the fallback, and the guard stops a second
  // change from submitting while the first navigation is underway.
  var quickRateForm = document.querySelector(".quick-rate-form");
  if (quickRateForm) {
    var quickRateBusy = false;
    var quickRateInputs = Array.prototype.slice.call(quickRateForm.querySelectorAll('input[name="rating"]'));
    var quickRateLabels = Array.prototype.slice.call(quickRateForm.querySelectorAll(".quick-half"));
    function quickRateValue(el) {
      return Number(el.htmlFor.slice(3));
    }
    function checkedRating() {
      for (var i = 0; i < quickRateInputs.length; i++) {
        if (quickRateInputs[i].checked) return Number(quickRateInputs[i].value);
      }
      return 0;
    }
    function clearDropping() {
      quickRateLabels.forEach(function (label) { label.classList.remove("dropping"); });
    }
    function previewDropped(hovered) {
      var current = checkedRating();
      quickRateLabels.forEach(function (label) {
        var value = quickRateValue(label);
        label.classList.toggle("dropping", current > 0 && hovered < current && value > hovered && value <= current);
      });
    }
    var quickRateClearSlot = quickRateForm.querySelector(".rating-clear-slot");
    if (quickRateClearSlot) {
      quickRateClearSlot.addEventListener("mouseenter", function () { previewDropped(0); });
    }
    quickRateLabels.forEach(function (label) {
      label.addEventListener("mouseenter", function () { previewDropped(quickRateValue(label)); });
    });
    // Keep the preview alive across the gaps between stars and the clear button;
    // only leaving the whole control restores the saved rating.
    quickRateForm.addEventListener("mouseleave", clearDropping);
    quickRateForm.addEventListener("submit", function (evt) {
      if (quickRateBusy) {
        evt.preventDefault();
        return;
      }
      quickRateBusy = true;
    });
    quickRateInputs.forEach(function (input) {
      input.addEventListener("change", function () {
        if (quickRateBusy) return;
        if (quickRateForm.requestSubmit) quickRateForm.requestSubmit();
        else quickRateForm.submit();
      });
    });
    window.addEventListener("pageshow", function () { quickRateBusy = false; });
  }

  // Theme toggle: dark ↔ light. "Match system" stays in Settings.
  var key = "luduslog-theme";
  function storedTheme() {
    try {
      var value = localStorage.getItem(key);
      return value === "light" || value === "dark" ? value : "";
    } catch (e) { return ""; }
  }
  function serverTheme() {
    var value = document.documentElement.getAttribute("data-theme");
    return value === "light" || value === "dark" || value === "contrast" ? value : "";
  }
  function currentTheme() {
    return storedTheme() || serverTheme() || "dark";
  }
  function labelTheme(mode) {
    var btn = document.getElementById("theme-toggle");
    if (!btn) return;
    btn.setAttribute("aria-label", "Theme: " + mode + ". Switch to " + (mode === "light" ? "dark" : "light") + " theme");
    btn.title = mode === "light" ? "Switch to dark theme" : "Switch to light theme";
  }
  function applyTheme(mode) {
    document.documentElement.setAttribute("data-theme", mode);
    labelTheme(mode);
  }
  var saved = storedTheme();
  if (saved) applyTheme(saved);

  var actions = document.querySelector(".header-actions");
  if (actions && !document.getElementById("theme-toggle")) {
    var btn = document.createElement("button");
    btn.id = "theme-toggle";
    btn.type = "button";
    btn.className = "icon-btn";
    btn.textContent = "◐";
    btn.addEventListener("click", function () {
      var next = currentTheme() === "light" ? "dark" : "light";
      try { localStorage.setItem(key, next); } catch (e) {}
      applyTheme(next);
    });
    actions.insertBefore(btn, actions.firstChild);
    labelTheme(currentTheme());
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

  // Favourite games: native drag-and-drop writes the new order into a hidden
  // field and submits the reorder form. Arrow buttons still work without JS.
  var favList = document.querySelector("[data-favorite-list]");
  if (favList) {
    var orderForm = document.querySelector("[data-favorite-order]");
    var orderInput = document.querySelector("[data-favorite-order-input]");
    var dragged = null;
    favList.addEventListener("dragstart", function (evt) {
      dragged = evt.target.closest("li[data-game-id]");
      if (!dragged) return;
      dragged.classList.add("dragging");
      if (evt.dataTransfer) evt.dataTransfer.effectAllowed = "move";
    });
    favList.addEventListener("dragend", function () {
      if (dragged) dragged.classList.remove("dragging");
      dragged = null;
    });
    favList.addEventListener("dragover", function (evt) {
      evt.preventDefault();
      var over = evt.target.closest("li[data-game-id]");
      if (!dragged || !over || over === dragged) return;
      var rect = over.getBoundingClientRect();
      var after = (evt.clientY - rect.top) > rect.height / 2;
      favList.insertBefore(dragged, after ? over.nextSibling : over);
    });
    favList.addEventListener("drop", function (evt) {
      evt.preventDefault();
      if (!orderForm || !orderInput) return;
      var ids = Array.prototype.map.call(favList.querySelectorAll("li[data-game-id]"), function (li) {
        return li.getAttribute("data-game-id");
      });
      orderInput.value = ids.join(",");
      orderForm.submit();
    });
  }

  // Related-games tabs: without JS the section lists every group under its
  // heading; with it only the selected tab's group is visible.
  document.querySelectorAll("[data-related-tabs]").forEach(function (section) {
    var tabs = Array.prototype.slice.call(section.querySelectorAll('[role="tab"]'));
    var panels = Array.prototype.slice.call(section.querySelectorAll('[role="tabpanel"]'));
    if (!tabs.length) return;
    section.setAttribute("data-enhanced", "");
    var seeAll = section.querySelector("[data-related-see-all]");
    function select(index, focus) {
      tabs.forEach(function (tab, i) {
        var on = i === index;
        tab.setAttribute("aria-selected", on ? "true" : "false");
        tab.tabIndex = on ? 0 : -1;
        if (on && focus) tab.focus();
      });
      panels.forEach(function (panel, i) { panel.hidden = i !== index; });
      if (seeAll) {
        var href = tabs[index].getAttribute("data-see-all");
        seeAll.hidden = !href;
        if (href) seeAll.setAttribute("href", href);
      }
    }
    tabs.forEach(function (tab, i) {
      tab.addEventListener("click", function () { select(i, false); });
      tab.addEventListener("keydown", function (evt) {
        var next = -1;
        if (evt.key === "ArrowRight") next = (i + 1) % tabs.length;
        else if (evt.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
        else if (evt.key === "Home") next = 0;
        else if (evt.key === "End") next = tabs.length - 1;
        if (next >= 0) { evt.preventDefault(); select(next, true); }
      });
    });
    select(0, false);
  });

  // Spoiler toggles.
  document.addEventListener("click", function (evt) {
    var btn = evt.target.closest("[data-spoiler-toggle]");
    if (!btn) return;
    var target = document.getElementById(btn.getAttribute("data-spoiler-toggle"));
    if (target) target.hidden = !target.hidden;
  });

  // Account menu: native details handles opening and keyboard use. Close it
  // when clicking elsewhere or pressing Escape.
  function closeUserMenus(except) {
    document.querySelectorAll(".user-menu[open]").forEach(function (menu) {
      if (menu !== except) menu.removeAttribute("open");
    });
  }
  document.addEventListener("click", function (evt) {
    var menu = evt.target.closest(".user-menu");
    closeUserMenus(menu);
  });
  document.addEventListener("keydown", function (evt) {
    if (evt.key !== "Escape") return;
    var open = document.querySelector(".user-menu[open]");
    if (!open) return;
    open.removeAttribute("open");
    var summary = open.querySelector("summary");
    if (summary) summary.focus();
  });

  // Screenshot lightbox. Without dialog support the thumbnail links open the
  // full image in a new tab exactly as they do without JavaScript.
  var lightbox = document.getElementById("screenshot-lightbox");
  var shots = Array.prototype.slice.call(document.querySelectorAll("[data-lightbox]"));
  if (lightbox && lightbox.showModal && shots.length) {
    var lightboxImg = lightbox.querySelector("[data-lightbox-image]");
    var lightboxCount = lightbox.querySelector("[data-lightbox-count]");
    var shotIndex = 0;
    function showShot(index) {
      shotIndex = (index + shots.length) % shots.length;
      var link = shots[shotIndex];
      lightboxImg.src = link.getAttribute("data-full") || link.href;
      lightboxImg.alt = link.getAttribute("data-alt") || "Screenshot";
      lightboxCount.textContent = (shotIndex + 1) + " / " + shots.length;
    }
    shots.forEach(function (link, index) {
      link.addEventListener("click", function (evt) {
        evt.preventDefault();
        showShot(index);
        lightbox.showModal();
      });
    });
    lightbox.querySelector("[data-lightbox-prev]").addEventListener("click", function () { showShot(shotIndex - 1); });
    lightbox.querySelector("[data-lightbox-next]").addEventListener("click", function () { showShot(shotIndex + 1); });
    lightbox.addEventListener("click", function (evt) {
      if (evt.target === lightbox) lightbox.close();
    });
    lightbox.addEventListener("keydown", function (evt) {
      if (evt.key === "ArrowLeft") { evt.preventDefault(); showShot(shotIndex - 1); }
      if (evt.key === "ArrowRight") { evt.preventDefault(); showShot(shotIndex + 1); }
    });
    lightbox.addEventListener("close", function () {
      lightboxImg.removeAttribute("src");
      lightboxImg.alt = "";
    });
  }

  // Game-page quick actions. Native dialogs keep the hero card height stable;
  // the buttons stay hidden without JavaScript and the noscript forms are used.
  var actionDialogs = document.querySelectorAll("dialog.game-action-dialog");
  document.querySelectorAll("[data-dialog-open]").forEach(function (trigger) {
    trigger.addEventListener("click", function () {
      var dialog = document.getElementById(trigger.getAttribute("data-dialog-open"));
      if (!dialog || !dialog.showModal) return;
      dialog.showModal();
    });
  });
  actionDialogs.forEach(function (dialog) {
    dialog.addEventListener("click", function (evt) {
      if (evt.target !== dialog) return;
      // Clicks on the dialog's own padding target the dialog too, so only
      // close when the pointer landed outside the dialog box (the backdrop).
      var box = dialog.getBoundingClientRect();
      if (evt.clientX < box.left || evt.clientX > box.right || evt.clientY < box.top || evt.clientY > box.bottom) {
        dialog.close();
      }
    });
  });

  // No-cookie video player: the iframe is created only after a click, and
  // removed on close so playback stops and no further requests are made.
  var videoDialog = document.getElementById("video-player");
  var videoLinks = document.querySelectorAll("[data-video-id]");
  if (videoDialog && videoDialog.showModal && videoLinks.length) {
    var videoFrame = videoDialog.querySelector("[data-video-frame]");
    videoLinks.forEach(function (link) {
      link.addEventListener("click", function (evt) {
        evt.preventDefault();
        var frame = document.createElement("iframe");
        frame.src = "https://www.youtube-nocookie.com/embed/" + encodeURIComponent(link.getAttribute("data-video-id")) + "?autoplay=1&rel=0";
        frame.title = link.textContent.trim();
        frame.allow = "autoplay; encrypted-media; picture-in-picture";
        frame.setAttribute("allowfullscreen", "");
        frame.setAttribute("referrerpolicy", "strict-origin-when-cross-origin");
        videoFrame.replaceChildren(frame);
        videoDialog.showModal();
      });
    });
    videoDialog.addEventListener("click", function (evt) {
      if (evt.target === videoDialog) videoDialog.close();
    });
    videoDialog.addEventListener("close", function () {
      videoFrame.replaceChildren();
    });
  }

  // Log editor tabs. Panels stay in the document so a single form submission
  // keeps every field; without JavaScript all panels remain visible.
  document.querySelectorAll("[data-log-tabs]").forEach(function (root) {
    var tabs = Array.prototype.slice.call(root.querySelectorAll("[data-log-tab]"));
    var panels = Array.prototype.slice.call(root.querySelectorAll("[data-log-panel]"));
    if (!tabs.length || !panels.length) return;
    root.setAttribute("data-enhanced", "");
    function select(key, focus) {
      tabs.forEach(function (tab) {
        var on = tab.getAttribute("data-log-tab") === key;
        tab.setAttribute("aria-selected", on ? "true" : "false");
        tab.tabIndex = on ? 0 : -1;
        if (on && focus) tab.focus();
      });
      panels.forEach(function (panel) {
        panel.hidden = panel.getAttribute("data-log-panel") !== key;
      });
      if (history.replaceState) {
        var url = new URL(window.location.href);
        url.searchParams.set("tab", key);
        history.replaceState(null, "", url.toString().replace(/\?tab=play$/, ""));
      }
    }
    var initial = "play";
    tabs.forEach(function (tab) {
      if (tab.getAttribute("aria-selected") === "true") initial = tab.getAttribute("data-log-tab");
    });
    var hash = (window.location.hash || "").replace("#", "");
    if (hash && tabs.some(function (tab) { return tab.getAttribute("data-log-tab") === hash; })) initial = hash;
    tabs.forEach(function (tab, i) {
      tab.addEventListener("click", function () { select(tab.getAttribute("data-log-tab"), false); });
      tab.addEventListener("keydown", function (evt) {
        var next = -1;
        if (evt.key === "ArrowRight") next = (i + 1) % tabs.length;
        else if (evt.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
        else if (evt.key === "Home") next = 0;
        else if (evt.key === "End") next = tabs.length - 1;
        if (next < 0) return;
        evt.preventDefault();
        select(tabs[next].getAttribute("data-log-tab"), true);
      });
    });
    select(initial, false);
  });

  // Journal day chips: fill any empty date inputs in the nearest form with
  // today's date, leaving dates the user already typed alone.
  document.querySelectorAll("[data-today]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var form = btn.closest("form");
      if (!form) return;
      var today = new Date().toISOString().slice(0, 10);
      form.querySelectorAll('input[type="date"]').forEach(function (input) {
        if (!input.value) input.value = today;
      });
    });
  });
})();
