(function () {
  function currentFileName(pathname) {
    var last = pathname.substring(pathname.lastIndexOf("/") + 1);
    if (!last || last === "index.html") {
      return "index.html";
    }
    return last;
  }

  function switchTarget() {
    var pathname = window.location.pathname;
    var isChinese =
      pathname.indexOf("/zh-CN/") !== -1 || pathname.endsWith("/zh-CN");
    var file = currentFileName(pathname);

    if (isChinese) {
      return file === "index.html" ? "../" : "../" + file;
    }
    return file === "index.html" ? "zh-CN/" : "zh-CN/" + file;
  }

  function addLanguageSwitch() {
    if (document.querySelector(".language-switch")) {
      return;
    }

    var pathname = window.location.pathname;
    var isChinese =
      pathname.indexOf("/zh-CN/") !== -1 || pathname.endsWith("/zh-CN");
    var rightButtons =
      document.querySelector("#mdbook-menu-bar .right-buttons") ||
      document.querySelector(".menu-bar .right-buttons") ||
      document.querySelector("#mdbook-menu-bar") ||
      document.querySelector(".menu-bar");
    if (!rightButtons) {
      return;
    }

    var link = document.createElement("a");
    link.className = "language-switch";
    link.href = switchTarget();
    link.title = isChinese ? "Switch to English" : "切换到中文";
    link.setAttribute("aria-label", link.title);
    link.innerHTML =
      '<span class="language-switch__icon" aria-hidden="true">🌐</span><span class="language-switch__label">' +
      (isChinese ? "EN" : "中文") +
      "</span>";

    rightButtons.insertBefore(link, rightButtons.firstChild);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", addLanguageSwitch);
  } else {
    addLanguageSwitch();
  }
})();
