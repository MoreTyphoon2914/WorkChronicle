(function (root, factory) {
  "use strict";
  root.WorkTrackerBrowserCore = factory();
}(this, function () {
  "use strict";

  var SCHEMA_VERSION = 1;

  function lower(value) {
    return String(value || "").toLowerCase();
  }

  function mediaType(element) {
    return lower(element && element.tagName) === "audio" ? "audio" : "video";
  }

  function elementState(element) {
    if (element.ended) {
      return "stopped";
    }
    if (element.paused) {
      return "paused";
    }
    return "playing";
  }

  function aggregateMedia(elements) {
    var list = elements || [];
    if (!list.length) {
      return { present: false, state: "none", type: "none", audible: false };
    }

    var state = "stopped";
    var selectedType = mediaType(list[0]);
    var audible = false;
    var i;
    for (i = 0; i < list.length; i += 1) {
      var currentState = elementState(list[i]);
      if (currentState === "playing") {
        if (state !== "playing") {
          selectedType = mediaType(list[i]);
        }
        state = "playing";
        if (!list[i].muted && Number(list[i].volume) > 0) {
          audible = true;
        }
      } else if (currentState === "paused" && state === "stopped") {
        state = "paused";
        selectedType = mediaType(list[i]);
      }
    }

    return { present: true, state: state, type: selectedType, audible: audible };
  }

  function domainFromUrl(rawUrl) {
    var value = String(rawUrl || "");
    if (!value) {
      return "";
    }
    try {
      if (typeof URL !== "undefined") {
        return lower(new URL(value).hostname).replace(/\.$/, "");
      }
    } catch (ignored) {
      return "";
    }

    var match = /^[A-Za-z][A-Za-z0-9+.-]*:\/\/([^\/?#]*)/.exec(value);
    if (!match) {
      return "";
    }
    var authority = match[1].replace(/^.*@/, "");
    if (authority.charAt(0) === "[") {
      var bracket = authority.indexOf("]");
      return bracket >= 0 ? lower(authority.substring(1, bracket)) : "";
    }
    return lower(authority.replace(/:\d+$/, "")).replace(/\.$/, "");
  }

  function normalizeMedia(media, tabAudible) {
    if (!media || !media.present) {
      return { present: false, state: "none", type: "none", audible: false };
    }
    var state = lower(media.state);
    var type = lower(media.type);
    return {
      present: true,
      state: state === "playing" || state === "paused" || state === "stopped" ? state : "unknown",
      type: type === "audio" || type === "video" ? type : "unknown",
      audible: state === "playing" && Boolean(media.audible || tabAudible)
    };
  }

  function buildObservation(input) {
    var url = String(input.url || "");
    return {
      schema_version: SCHEMA_VERSION,
      browser: lower(input.browser),
      tab_id: String(input.tabId),
      active: Boolean(input.active),
      visible: Boolean(input.visible),
      url: url,
      domain: domainFromUrl(url),
      title: String(input.title || ""),
      media: normalizeMedia(input.media, input.tabAudible),
      observed_at: input.observedAt || new Date().toISOString()
    };
  }

  function meaningfulFingerprint(observation) {
    var media = observation.media || {};
    return [
      observation.schema_version,
      observation.browser,
      observation.tab_id,
      observation.active,
      observation.visible,
      observation.url,
      observation.domain,
      observation.title,
      media.present,
      media.state,
      media.type,
      media.audible
    ].join("\u001f");
  }

  function hasMeaningfulChange(previous, next) {
    return !previous || meaningfulFingerprint(previous) !== meaningfulFingerprint(next);
  }

  function shouldSendObservation(previous, next, terminal) {
    var previousMedia = previous && previous.media;
    return Boolean(
      terminal ||
      next.active ||
      (next.media && next.media.present) ||
      (previous && previous.active) ||
      (previousMedia && previousMedia.present)
    );
  }

  return {
    SCHEMA_VERSION: SCHEMA_VERSION,
    aggregateMedia: aggregateMedia,
    buildObservation: buildObservation,
    domainFromUrl: domainFromUrl,
    hasMeaningfulChange: hasMeaningfulChange,
    meaningfulFingerprint: meaningfulFingerprint,
    normalizeMedia: normalizeMedia,
    shouldSendObservation: shouldSendObservation
  };
}));
