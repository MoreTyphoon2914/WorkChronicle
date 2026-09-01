(function () {
  "use strict";

  var fso = new ActiveXObject("Scripting.FileSystemObject");
  var testDir = fso.GetParentFolderName(WScript.ScriptFullName);
  var sourcePath = fso.BuildPath(fso.GetParentFolderName(testDir), "src\\core.js");
  var file = fso.OpenTextFile(sourcePath, 1, false);
  new Function(file.ReadAll())();
  file.Close();

  var core = WorkTrackerBrowserCore;
  var failures = 0;

  function assert(name, condition) {
    if (!condition) {
      WScript.Echo("FAIL " + name);
      failures += 1;
    } else {
      WScript.Echo("PASS " + name);
    }
  }

  assert("Firefox identity", core.detectBrowserFamily("Mozilla/5.0 Firefox/142.0") === "firefox");
  assert("Chrome identity", core.detectBrowserFamily("Mozilla/5.0 Chrome/140.0.0.0 Safari/537.36") === "chrome");
  assert("Edge identity", core.detectBrowserFamily("Mozilla/5.0 Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0") === "edge");

  function media(tag, paused, ended, muted, volume) {
    return { tagName: tag, paused: paused, ended: ended, muted: muted, volume: volume };
  }

  var none = core.aggregateMedia([]);
  assert("no media", !none.present && none.state === "none" && none.type === "none" && !none.audible);

  var paused = core.aggregateMedia([media("VIDEO", true, false, false, 1)]);
  assert("paused video", paused.present && paused.state === "paused" && paused.type === "video" && !paused.audible);

  var stopped = core.aggregateMedia([media("AUDIO", true, true, false, 1)]);
  assert("ended audio", stopped.present && stopped.state === "stopped" && stopped.type === "audio");

  var mixed = core.aggregateMedia([
    media("VIDEO", true, false, false, 1),
    media("AUDIO", false, false, false, 0.5)
  ]);
  assert("playing wins aggregation", mixed.state === "playing" && mixed.type === "audio" && mixed.audible);

  var muted = core.aggregateMedia([media("VIDEO", false, false, true, 1)]);
  assert("muted playing is not audible", muted.state === "playing" && !muted.audible);

  assert("domain strips port and case", core.domainFromUrl("https://Example.COM:8443/path?q=1") === "example.com");
  assert("domain handles credentials", core.domainFromUrl("https://user:pass@example.org/x") === "example.org");
  assert("invalid relative URL has no domain", core.domainFromUrl("/relative") === "");

  var input = {
    browser: "Edge",
    tabId: 42,
    active: false,
    visible: false,
    url: "https://media.example/video",
    title: "Course",
    media: mixed,
    tabAudible: false,
    observedAt: "2026-08-20T10:00:00.000Z"
  };
  var observation = core.buildObservation(input);
  assert("payload schema", observation.schema_version === 1 && observation.browser === "edge" && observation.tab_id === "42");
  assert("payload context", !observation.active && !observation.visible && observation.domain === "media.example" && observation.title === "Course");
  assert("background playback retained", observation.media.state === "playing" && observation.media.audible);

  var heartbeat = core.buildObservation(input);
  heartbeat.observed_at = "2026-08-20T10:00:05.000Z";
  assert("timestamp-only heartbeat is not meaningful change", !core.hasMeaningfulChange(observation, heartbeat));
  heartbeat.media.state = "paused";
  heartbeat.media.audible = false;
  assert("media transition is meaningful change", core.hasMeaningfulChange(observation, heartbeat));

  var cleared = core.buildObservation({
    browser: "edge",
    tabId: 42,
    active: false,
    visible: false,
    url: input.url,
    title: input.title,
    media: none,
    observedAt: "2026-08-20T10:00:06.000Z"
  });
  assert("inactive media removal emits clearing observation", core.shouldSendObservation(observation, cleared, false));
  assert("irrelevant inactive no-media heartbeat is skipped", !core.shouldSendObservation(cleared, cleared, false));

  if (failures === 0) {
    WScript.Echo("ALL TESTS PASSED");
  }
  WScript.Quit(failures === 0 ? 0 : 1);
}());
