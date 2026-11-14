(function () {
  var button = document.querySelector("[data-copy-password]");
  var value = button && button.querySelector(".generated-password-value");
  var status = document.getElementById("password-copy-status");
  if (!button || !value) return;

  var resetTimer;
  var copying = false;

  function showCopied() {
    button.classList.add("is-copied");
    if (status) status.textContent = "Copied";
    clearTimeout(resetTimer);
    resetTimer = setTimeout(function () {
      button.classList.remove("is-copied");
      if (status) status.textContent = "";
    }, 1600);
  }

  function fallbackCopy(text) {
    var area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.top = "0";
    area.style.left = "0";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.focus();
    area.select();
    var ok = false;
    try {
      ok = document.execCommand("copy");
    } catch (err) {
      ok = false;
    }
    document.body.removeChild(area);
    button.focus();
    return ok;
  }

  function copyPassword(allowFallback) {
    if (copying) return;
    var text = value.textContent;
    if (!text) return;
    copying = true;
    var finish = function (ok) {
      copying = false;
      if (ok) showCopied();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        finish(true);
      }, function () {
        finish(allowFallback && fallbackCopy(text));
      });
      return;
    }
    finish(allowFallback && fallbackCopy(text));
  }

  button.addEventListener("pointerenter", function () {
    copyPassword(false);
  });
  button.addEventListener("click", function () {
    copyPassword(true);
  });
})();
