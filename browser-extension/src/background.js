(function (root) {
  "use strict";

  if (typeof root.WorkTrackerBrowserCore === "undefined" && typeof importScripts === "function") {
    importScripts("core.js", "compat.js");
  }

  var core = root.WorkTrackerBrowserCore;
  var compat = root.WorkTrackerBrowserCompat;
  var api = compat.api;
  var endpoint = "http://127.0.0.1:5601/api/v1/browser/observations";
  var states = Object.create(null);
  var lastSent = Object.create(null);
  var lastFailureLog = 0;

  function detectBrowser() {
    var agent = String(navigator.userAgent || "").toLowerCase();
    if (agent.indexOf("firefox/") >= 0) { return "firefox"; }
    if (agent.indexOf("edg/") >= 0) { return "edge"; }
    return "chrome";
  }

  function emptyMedia() {
    return { present: false, state: "none", type: "none", audible: false };
  }

  function observationFor(tab, page, terminal) {
    page = page || {};
    return core.buildObservation({
      browser: detectBrowser(),
      tabId: tab.id,
      active: terminal ? false : tab.active,
      visible: terminal ? false : page.visible,
      url: terminal ? (page.url || tab.url) : (tab.url || page.url),
      title: terminal ? (page.title || tab.title) : (tab.title || page.title),
      media: terminal ? emptyMedia() : (page.media || emptyMedia()),
      tabAudible: terminal ? false : tab.audible,
      observedAt: new Date().toISOString()
    });
  }

  function post(observation) {
    return fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(observation),
      cache: "no-store"
    }).then(function (response) {
      if (!response.ok) {
        throw new Error("browser observation rejected with HTTP " + response.status);
      }
    }).catch(function (error) {
      var now = Date.now();
      if (now - lastFailureLog >= 60000) {
        console.warn("WorkChronicle browser endpoint unavailable:", error.message);
        lastFailureLog = now;
      }
    });
  }

  function send(tab, page, force, terminal) {
    var observation = observationFor(tab, page, terminal);
    var key = observation.tab_id;
    var relevant = core.shouldSendObservation(lastSent[key], observation, terminal);
    if (!relevant) {
      return Promise.resolve();
    }
    if (!force && !core.hasMeaningfulChange(lastSent[key], observation)) {
      return Promise.resolve();
    }
    lastSent[key] = observation;
    return post(observation);
  }

  function updateTabs(forceHeartbeat) {
    return compat.tabsQuery({}).then(function (tabs) {
      var tasks = [];
      tabs.forEach(function (tab) {
        var key = String(tab.id);
        var existing = states[key];
        var activityChanged = existing && Boolean(existing.tab.active) !== Boolean(tab.active);
        if (!existing) {
          existing = { tab: tab, page: null };
          states[key] = existing;
        } else {
          existing.tab = tab;
        }
        if (tab.active || (existing.page && existing.page.media && existing.page.media.present) || activityChanged) {
          tasks.push(send(tab, existing.page, Boolean(forceHeartbeat || activityChanged), false));
        }
      });
      return Promise.all(tasks);
    }).catch(function () {
      // Temporary extension API failures must not stop later heartbeats.
    });
  }

  api.runtime.onMessage.addListener(function (message, sender) {
    if (!message || message.type !== "worktracker:page-state" || !sender.tab) {
      return false;
    }
    var key = String(sender.tab.id);
    var previous = states[key];
    var page = message.payload || {};
    if (page.closing) {
      send(sender.tab, previous ? previous.page : page, true, true);
      delete states[key];
      return false;
    }
    states[key] = { tab: sender.tab, page: page };
    send(sender.tab, page, false, false);
    return false;
  });

  api.tabs.onActivated.addListener(function () { updateTabs(false); });

  api.tabs.onUpdated.addListener(function (tabId, changeInfo, tab) {
    var key = String(tabId);
    var previous = states[key];
    if ((changeInfo.status === "loading" || changeInfo.url) && previous) {
      send(previous.tab || tab, previous.page, true, true);
      delete states[key];
    }
    if (tab.active) {
      var current = states[key] || { tab: tab, page: null };
      current.tab = tab;
      states[key] = current;
      send(tab, current.page, true, false);
    }
  });

  api.tabs.onRemoved.addListener(function (tabId) {
    var key = String(tabId);
    var previous = states[key];
    if (previous) {
      send(previous.tab, previous.page, true, true);
    }
    delete states[key];
    delete lastSent[key];
  });

  setInterval(function () { updateTabs(true); }, 5000);
  updateTabs(true);
}(this));
