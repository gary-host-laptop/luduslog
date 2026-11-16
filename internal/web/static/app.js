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
})();
