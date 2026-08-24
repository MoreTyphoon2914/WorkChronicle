(function () {
  "use strict";

  var core = WorkTrackerBrowserCore;
  var compat = WorkTrackerBrowserCompat;
  var lastFingerprint = "";
  var timer = null;

  function pageState(closing) {
    return {
      visible: document.visibilityState === "visible",
      url: window.location.href,
      title: document.title || "",
      media: core.aggregateMedia(Array.prototype.slice.call(document.querySelectorAll("video,audio"))),
      closing: Boolean(closing)
    };
  }

  function emit(force, closing) {
    var state = pageState(closing);
    var fingerprint = JSON.stringify(state);
    if (!force && fingerprint === lastFingerprint) {
      return;
    }
    lastFingerprint = fingerprint;
    compat.runtimeSendMessage({ type: "worktracker:page-state", payload: state }).catch(function () {
      // A navigation can tear down the messaging channel; the background
      // navigation listener also emits a terminal observation.
    });
  }

  function schedule() {
    if (timer !== null) {
      clearTimeout(timer);
    }
    timer = setTimeout(function () {
      timer = null;
      emit(false, false);
    }, 50);
  }

  ["play", "playing", "pause", "ended", "emptied", "loadedmetadata", "volumechange"].forEach(function (name) {
    document.addEventListener(name, schedule, true);
  });
  document.addEventListener("visibilitychange", schedule, true);
  window.addEventListener("hashchange", schedule, true);
  window.addEventListener("popstate", schedule, true);
  window.addEventListener("pagehide", function () { emit(true, true); }, true);

  new MutationObserver(schedule).observe(document.documentElement || document, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["src", "muted", "title"]
  });

  // Covers title and History API changes that do not create DOM mutations.
  setInterval(schedule, 1000);
  emit(true, false);
}());
