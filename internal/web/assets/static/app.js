// Minimal, dependency-free dashboard behavior. No build step, no framework
// (roadmap: embed-only, single-binary). Each feature is an isolated guard so the
// same file is safe to load on pages that lack a given element (e.g. /login).
(function () {
  "use strict";

  // Auto-refresh: re-fetch the whole page on a timer. The JSON APIs
  // (/api/fleet, /api/nodes/{id}) exist for tooling and future in-place updates.
  var box = document.getElementById("autorefresh");
  if (box) {
    var KEY = "scootship.autorefresh";
    var INTERVAL_MS = 5000;
    var timer = null;
    var saved = localStorage.getItem(KEY);
    if (saved !== null) box.checked = saved === "1";
    var schedule = function () {
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      if (box.checked && !document.hidden) {
        timer = setTimeout(function () {
          location.reload();
        }, INTERVAL_MS);
      }
    };
    box.addEventListener("change", function () {
      localStorage.setItem(KEY, box.checked ? "1" : "0");
      schedule();
    });
    document.addEventListener("visibilitychange", schedule);
    schedule();
  }

  // Collapsible sidebar. On wide screens it collapses to an icon rail (remembered
  // in localStorage); on narrow screens it slides in as an overlay.
  var toggle = document.getElementById("sidebarToggle");
  if (toggle) {
    var SKEY = "scootship.sidebar";
    var body = document.body;
    var scrim = document.getElementById("scrim");
    var narrow = function () {
      return window.matchMedia("(max-width: 900px)").matches;
    };
    if (localStorage.getItem(SKEY) === "collapsed")
      body.classList.add("sidebar-collapsed");
    toggle.addEventListener("click", function () {
      if (narrow()) {
        body.classList.toggle("sidebar-open");
      } else {
        body.classList.toggle("sidebar-collapsed");
        localStorage.setItem(
          SKEY,
          body.classList.contains("sidebar-collapsed") ? "collapsed" : "open",
        );
      }
    });
    if (scrim) {
      scrim.addEventListener("click", function () {
        body.classList.remove("sidebar-open");
      });
    }
  }
})();
