(function (root) {
  "use strict";

  var api = typeof browser !== "undefined" ? browser : chrome;
  var promiseAPI = typeof browser !== "undefined";

  function call(target, method, args) {
    if (promiseAPI) {
      return Promise.resolve(target[method].apply(target, args || []));
    }
    return new Promise(function (resolve, reject) {
      var actual = (args || []).slice();
      actual.push(function (result) {
        var error = api.runtime.lastError;
        if (error) {
          reject(new Error(error.message));
          return;
        }
        resolve(result);
      });
      target[method].apply(target, actual);
    });
  }

  root.WorkTrackerBrowserCompat = {
    api: api,
    runtimeSendMessage: function (message) {
      return call(api.runtime, "sendMessage", [message]);
    },
    tabsGet: function (tabId) {
      return call(api.tabs, "get", [tabId]);
    },
    tabsQuery: function (query) {
      return call(api.tabs, "query", [query]);
    }
  };
}(this));
